package accesshttp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Site passcodes.
//
// An owner may put one passcode on a whole site. Every address of the site
// then answers an "Enter the passcode" page (401, noindex, nothing of the
// site in it; a request that is not a page still answers the plain line
// "This site is protected") until the visitor enters the passcode; after that an
// unlock cookie for that one host lets them in until the passcode changes or
// the owner signs everyone out. The saved-data API of a protected site takes
// that same cookie, or the owner's (or the admin's) key; previews bypass it
// (their token is the capability).
//
// Storage: the passcode is sealed with AES-256-GCM under PASSCODE_ENC_KEY
// (sites.passcode_enc; the site id is the associated data), so the owner can
// read it back. It is checked by opening the sealed value and comparing in
// constant time. A slow hash on top would protect nothing: whoever holds the
// key can open the sealed value anyway, and without the key neither the
// sealed value nor a hash is of use (a hash of a 6-digit code falls to a
// dictionary in seconds). sites.view_password_hash (the removed July
// feature) is never read.
//
// Unlock cookie: __Host-sh_pass_<16 hex of sha256(site id)> (a plain
// sh_pass_... over HTTP), host-only, HttpOnly, SameSite=Lax, Max-Age 400
// days (the browsers' cap). Its value is v1.<expiry>.<generation>.<mac>, the
// MAC an HMAC-SHA256 over the site id, the host, the expiry and the
// generation under a key derived from PASSCODE_ENC_KEY. The generation
// (sites.passcode_generation) goes up on every change and on "sign everyone
// out", which ends every unlock at once.
//
// Where it is enforced: serveSiteFile (every Go path to a site's files) and
// the `passcode` marker file next to `current`, which makes nginx and Caddy
// hand disk-served addresses (custom domains, the content host) to
// /internal/passcode instead of serving the file. The marker is written
// before the database on lock and removed after it on unlock, so a failure
// in between leaves the site locked, never open. Take-down (410) and offline
// (503) win over the passcode (401).

// passcodeCookieMaxAge is the unlock's life: 400 days, the longest Max-Age
// browsers keep. An unlock never ends on its own before that; a new
// passcode or "sign everyone out" ends it.
const passcodeCookieMaxAge = 400 * 24 * time.Hour

// passcodeMaxLength is the longest passcode (in characters).
const passcodeMaxLength = 128

// passcodeState is the server key and the brute-force counters.
type passcodeState struct {
	// limits is the handler's own snapshot (h.limits), copied in once at
	// construction (NewSiteHandlers) since the rate-limit methods below have
	// no AccessHandler of their own. Zero in a bare notifyClient{secrets: &passcodeState{}} test literal, which
	// never calls wrongTry/sweep.
	Limits edition.Limits
	// keys are the PASSCODE_ENC_KEY keys (SetPasscodeKey).
	keys accounts.PasscodeKeys

	mu        sync.Mutex
	ipTries   *httpx.RateLimiter   // site id + "|" + client address
	siteTries *httpx.RateLimiter   // site id
	locked    map[string]time.Time // "ip:" or "site:" + key: refused until
	Now       func() time.Time
	// ipSalt makes the logged address hash unlinkable across restarts.
	ipSalt []byte
}

// SetPasscodeKey reads PASSCODE_ENC_KEY (accounts.PasscodeKeys.Set).
func (h *AccessHandler) SetPasscodeKey(b64 string) error { return h.Passcode.keys.Set(b64) }

// PasscodeKeys is the passcode key set Data reads for notification secrets.
func (h *AccessHandler) PasscodeKeys() *accounts.PasscodeKeys { return &h.Passcode.keys }

// VisitorRefKey keys the connector's per-site visitor references
// (accounts.PasscodeKeys.VisitorRef).
func (h *AccessHandler) VisitorRefKey() []byte { return h.Passcode.keys.VisitorRef(h.ExportKey) }

// PasscodesReady reports whether a passcode can be set on this server:
// SITE_PASSCODES is on, the key is set and sites have addresses of their own.
func (h *AccessHandler) PasscodesReady() bool {
	return h.Limits.SitePasscodes && h.Passcode.keys.AEAD != nil && !h.Hosts.Addressing().SharedOrigin()
}

func (p *passcodeState) clock() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// seal encrypts a passcode for one site.
func (p *passcodeState) Seal(siteID, code string) ([]byte, error) {
	return p.sealWithAD("site:"+siteID, code)
}

func (p *passcodeState) sealWithAD(ad, code string) ([]byte, error) {
	if p.keys.AEAD == nil {
		return nil, errPasscodeNoKey
	}
	nonce := make([]byte, p.keys.AEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.keys.AEAD.Seal(nonce, nonce, []byte(code), []byte(ad)), nil
}

// open decrypts a site's sealed passcode.
func (p *passcodeState) open(siteID string, enc []byte) (string, error) {
	return p.openWithAD("site:"+siteID, enc)
}

func (p *passcodeState) openWithAD(ad string, enc []byte) (string, error) {
	if p.keys.AEAD == nil {
		return "", errPasscodeNoKey
	}
	n := p.keys.AEAD.NonceSize()
	if len(enc) < n+p.keys.AEAD.Overhead() {
		return "", errPasscodeSealed
	}
	pt, err := p.keys.AEAD.Open(nil, enc[:n], enc[n:], []byte(ad))
	if err != nil {
		return "", errPasscodeSealed
	}
	return string(pt), nil
}

var (
	errPasscodeNoKey  = errors.New("PASSCODE_ENC_KEY is not set")
	errPasscodeSealed = errors.New("the stored passcode cannot be opened with PASSCODE_ENC_KEY")
)

// passcodeMatches compares a try with the real passcode in constant time
// (over digests, so the length is not measured either).
func passcodeMatches(try, real string) bool {
	a := sha256.Sum256([]byte(try))
	b := sha256.Sum256([]byte(real))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// normalizePasscode trims surrounding white space and checks the length.
// There are no other rules: any characters, digits only included. Control characters are refused because the gate's
// one-line box could never send them.
func normalizePasscode(code string, min int) (string, error) {
	code = strings.TrimSpace(code)
	if !utf8.ValidString(code) {
		return "", errors.New("the passcode is not valid text")
	}
	n := utf8.RuneCountInString(code)
	if n < min {
		return "", fmt.Errorf("a passcode needs at least %d characters", min)
	}
	if n > passcodeMaxLength {
		return "", fmt.Errorf("a passcode can be at most %d characters", passcodeMaxLength)
	}
	for _, r := range code {
		if unicode.IsControl(r) {
			return "", errors.New("a passcode cannot hold line breaks, tabs or other control characters")
		}
	}
	return code, nil
}

// generatePasscode makes a random passcode of PASSCODE_MIN_LENGTH digits (at
// least 6).
func generatePasscode(n int) (string, error) {
	if n < 6 {
		n = 6
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String(), nil
}

// --- The unlock cookie ---

// passcodeCookieName is the per-site cookie name. Over HTTPS only the
// __Host- form is ever read: a sibling host can plant a Domain cookie of a
// plain name, never a __Host- one.
func passcodeCookieName(r *http.Request, siteID string) string {
	sum := sha256.Sum256([]byte(siteID))
	name := "sh_pass_" + hex.EncodeToString(sum[:8])
	if httpx.IsHTTPS(r) {
		return "__Host-" + name
	}
	return name
}

func (p *passcodeState) cookieMAC(siteID, host string, exp int64, gen int) string {
	m := hmac.New(sha256.New, p.keys.MACKey)
	fmt.Fprintf(m, "v1|%s|%s|%d|%d", siteID, strings.ToLower(host), exp, gen)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setUnlockCookie lets this browser into the site on this host.
func (h *AccessHandler) setUnlockCookie(w http.ResponseWriter, r *http.Request, siteID string, gen int) {
	exp := h.Passcode.clock().Add(passcodeCookieMaxAge).Unix()
	value := "v1." + strconv.FormatInt(exp, 10) + "." + strconv.Itoa(gen) + "." + h.Passcode.cookieMAC(siteID, httpx.HostName(r), exp, gen)
	http.SetCookie(w, &http.Cookie{
		Name:     passcodeCookieName(r, siteID),
		Value:    value,
		Path:     "/",
		MaxAge:   int(passcodeCookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   httpx.IsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// unlocked reports whether the request carries a valid unlock cookie for
// this site, on this host, for the passcode's current generation.
func (h *AccessHandler) unlocked(r *http.Request, siteID string, gen int) bool {
	if h.Passcode.keys.MACKey == nil {
		return false
	}
	c, err := r.Cookie(passcodeCookieName(r, siteID))
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return false
	}
	exp, err1 := strconv.ParseInt(parts[1], 10, 64)
	g, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || g != gen || exp < h.Passcode.clock().Unix() {
		return false
	}
	want := h.Passcode.cookieMAC(siteID, httpx.HostName(r), exp, g)
	return hmac.Equal([]byte(parts[3]), []byte(want))
}

// crossSiteSubresource: a request some other site's page made for a file
// (a <script>, <img>, fetch), not a navigation. Fetch metadata is set by
// browsers only; a request without it is a navigation or a non-browser.
func crossSiteSubresource(r *http.Request) bool {
	sfs := r.Header.Get("Sec-Fetch-Site")
	if sfs == "" || sfs == "same-origin" || sfs == "none" {
		return false
	}
	return r.Header.Get("Sec-Fetch-Mode") != "navigate"
}

// --- The gate on the file-serving path ---

// SiteGate is the visitor gate every Go path to a site's files goes through:
// the site handler puts a sites.Serving on the request, the gate answers the
// take-down, offline, passcode or named-viewers page when one applies, and
// otherwise hands the request to Serving.Then. A protected site that lets the
// request in gets its private-cache headers here.
func (h *AccessHandler) SiteGate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := appsites.ServingOf(r)
		if !ok || s.UserID == "" {
			h.servePasscodeGate(w, r, "", "", 0) // a protected request on a host the app does not know
			return
		}
		if !s.FrameOnly && h.GateRefuses(w, r, s) {
			return
		}
		h.ownerFrameGuard(w, r, s.UserID)
		s.Then.ServeHTTP(w, r)
	})
}

// GateRefuses reads the opened folder's markers, measures the visitor, asks
// accounts.DecideVisit and renders every verdict but serve as a page.
func (h *AccessHandler) GateRefuses(w http.ResponseWriter, r *http.Request, s appsites.Serving) bool {
	present := func(name string) bool { _, err := s.Root.Lstat(name); return err == nil }
	v := accounts.Visit{Kind: accounts.VisitPage, Suspended: present("suspended"), Offline: present("offline"), Protected: present("passcode"), Bound: true,
		SharedHost: h.Hosts.IsSharedHost(httpx.HostName(r))}
	var sess model.VisitorSession
	var email string
	if v.Protected && !v.Suspended && !v.Offline {
		v.Row, v.RowErr = h.siteProtectionRow(r, s.Root, s.UserID, s.Name)
		if v.RowErr == nil && !v.SharedHost {
			v.Who, sess, email = h.visitorFacts(r, v.Row, accounts.VisitPage, false)
		}
	}
	verdict := accounts.DecideVisit(v)
	gated := v.RowErr == nil && (v.Row.Enc != nil || v.Row.NamedViewers)
	if gated {
		protectedHeaders(w)
	}
	if v.Row.NamedViewers && (verdict == accounts.VisitServe || verdict == accounts.VisitNeedPasscode) {
		// Past the named-viewers rule.
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
			_ = h.StoreAccounts().TouchVisitorSession(r.Context(), sess.ID, h.Limits.VisitorSessionIdle)
		}
		// Browsers without fetch metadata still honour this: only the
		// site's own pages may frame it.
		if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
			w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
		}
	}
	switch verdict {
	case accounts.VisitServe:
		return false
	case accounts.VisitTakedown:
		web.ServeTakedownPage(w, r, h.pipeline())
	case accounts.VisitOffline:
		web.ServeOfflinePage(w, r, h.pipeline())
	case accounts.VisitNotFound:
		http.NotFound(w, r)
	case accounts.VisitServiceError:
		if v.RowErr != nil {
			log.Printf("passcode: site %s/%s: %v", s.UserID, s.Name, v.RowErr)
		} else {
			log.Printf("viewers: site %s: %v", v.Row.SiteID, v.Who.ListErr)
		}
		web.RenderServiceErrorPage(w, r, h.pipeline())
	case accounts.VisitElsewhere:
		h.passcodeElsewhere(w, r, v.Row, s.Rel)
	case accounts.VisitFramedOut:
		writeOAuthHTMLError(h.pipeline(), w, http.StatusForbidden)
	case accounts.VisitNeedSignIn:
		if !h.startGateSSO(w, r) {
			h.serveViewerPage(w, r, viewerPageSignIn, gateNext(r), "", "")
		}
	case accounts.VisitNotListed:
		// The owner hears about it once a day per person, for a page visit
		// only (a stray subresource is not someone asking to be let in).
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
			h.emitSiteEvent(appsites.SiteEvent{SiteID: v.Row.SiteID, Name: "viewer.access_requested", Data: map[string]any{"visitor": sess.UserID}, Dedupe: 24 * time.Hour})
		}
		h.serveViewerPage(w, r, viewerPagePrivate, gateNext(r), email, "", h.ssoBaseURL)
	case accounts.VisitNeedPasscode:
		h.servePasscodeGate(w, r, gateNext(r), "", 0)
	}
	return true
}

// visitorFacts measures what the request carries toward a protected site,
// for accounts.DecideVisit: only the facts the row and the request make
// relevant are looked up; keys says whether the request's key counts (never
// for a page, nor for a caller storage classed as a visitor). It also returns the visitor's session and, for a signed-in person, the
// address they present (the private page names it).
func (h *AccessHandler) visitorFacts(r *http.Request, row model.SitePasscodeRow, kind accounts.VisitKind, keys bool) (who accounts.Visitor, sess model.VisitorSession, email string) {
	pass := r.Header.Get(VisitorPassHeader) != ""
	who = accounts.Visitor{
		Read:       r.Method == http.MethodGet || r.Method == http.MethodHead,
		CrossSite:  crossSiteSubresource(r),
		Framed:     framedElsewhere(r),
		SameOrigin: pass || httpx.SameOrigin(r),
	}
	if kind != accounts.VisitPage {
		if keys {
			who.OwnerKey, who.AdminKey = h.KeyActsFor(r, row)
		}
		who.Preview = who.Read && h.previewRefererFor(r, row.SiteID)
	}
	if row.Enc != nil {
		who.Unlocked = h.unlocked(r, row.SiteID, row.Generation)
	}
	// The session and the list are looked up only when the named-viewers rule
	// will read them, so a request costs the queries it did before the gate
	// was one decision.
	needSession := !who.CrossSite && !who.Framed
	if kind != accounts.VisitPage {
		needSession = !who.OwnerKey && !who.AdminKey && !(who.Read && who.Preview) && (who.SameOrigin || who.Read && needSession)
	}
	if row.NamedViewers && kind != accounts.VisitSignIn && needSession {
		sess, who.Session = h.viewerSession(r, row.SiteID)
		if who.Session {
			if kind != accounts.VisitPage && who.SameOrigin && !pass {
				papermetrics.From(r.Context()).Member(r.Context(), sess.UserID)
			}
			who.Listed, email, who.ListErr = h.viewerAllowed(r, row, sess.UserID)
		}
	}
	return who, sess, email
}

// ownerFrameGuard: a page the site's owner opens while signed in on this
// host is never shown inside another origin's frame. There the page acts
// with owner rights for saved data (storageOwnerOnSite), and every
// <site>.<handle>.simple-host.app host is one "site" to a browser, so the
// Lax session cookie goes along when a sibling site frames it (clickjacking).
// Other visitors' pages are unchanged.
//
// On Simple Hack the owner is the team's members or the event's organisers
// (storageOwnerOnSite), and sibling team sites share one registrable domain
// the same way.
func (h *AccessHandler) ownerFrameGuard(w http.ResponseWriter, r *http.Request, ownerID string) {
	// A copy cached without the cookie must not stand in for one with it.
	w.Header().Add("Vary", "Cookie")
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "empty", "image", "script", "style", "font", "audio", "video", "track", "manifest", "worker", "sharedworker", "serviceworker":
		return // never framed (empty: fetch and XHR); no database lookup per subresource
	}
	sess, err := visitorCookieSession(r, h.Database, "", h.Limits.KeyIdleExpiry)
	if err != nil {
		return
	}
	if h.storageBindings != nil {
		papermetrics.From(r.Context()).Member(r.Context(), sess.UserID)
		if ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err != nil || !ok {
			return
		}
	} else if sess.UserID != ownerID {
		// A member of the team that holds the site, or an editor of it, is
		// an owner here too (accounts.ActsAsOwner, through the default
		// provider); a failed lookup keeps the frame guard.
		if ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err == nil && !ok {
			return
		}
	}
	setOwnerFrameHeaders(w)
}

// protectedHeaders: nothing of a protected (or named-viewers) site is ever
// stored by a shared cache, served to another site's page, or indexed.
func protectedHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Add("Vary", "Cookie")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

// siteProtectionRow binds the by-name lookup to the directory being served.
// Rename and deletion hold this same lock while moving the directory and row;
// neither may change the binding between the identity check and the query.
func (h *AccessHandler) siteProtectionRow(r *http.Request, root *os.Root, userID, siteName string) (model.SitePasscodeRow, error) {
	if root != nil {
		// Exports and publication share this mutex. Bound the visitor's wait
		// without leaving a goroutine behind to acquire it after cancellation.
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		mu, _ := h.UploadLocks.LoadOrStore(userID+"/"+siteName, &sync.Mutex{})
		m := mu.(*sync.Mutex)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if err := ctx.Err(); err != nil {
				return model.SitePasscodeRow{}, err
			}
			if m.TryLock() {
				break
			}
			select {
			case <-ctx.Done():
				return model.SitePasscodeRow{}, ctx.Err()
			case <-tick.C:
			}
		}
		defer m.Unlock()
		opened, err := root.Stat(".")
		if err != nil {
			return model.SitePasscodeRow{}, err
		}
		current, err := os.Stat(h.Disk.SiteDir(userID, siteName))
		if os.IsNotExist(err) {
			return model.SitePasscodeRow{}, sql.ErrNoRows
		}
		if err != nil {
			return model.SitePasscodeRow{}, err
		}
		if !os.SameFile(opened, current) {
			return model.SitePasscodeRow{}, sql.ErrNoRows
		}
	}
	return h.sitesSvc().GetSitePasscodeByName(r.Context(), userID, siteName)
}

// passcodeElsewhere answers a protected site's file on a shared host: a 302
// to the same path on the site's own address, or, when it has none, the
// plain protected page without a form.
func (h *AccessHandler) passcodeElsewhere(w http.ResponseWriter, r *http.Request, row model.SitePasscodeRow, rel string) {
	escRel := "/" + strings.TrimLeft((&url.URL{Path: rel}).EscapedPath(), "/")
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	target := ""
	if info, has, err := h.Hosts.SiteOwnAddress(r.Context(), row.SiteID); err == nil && has {
		target = "https://" + strings.ToLower(info.Domain) + escRel
	} else if handle, _, name, err := h.sitesSvc().GetSiteOwner(r.Context(), row.SiteID); err == nil && handle != "" && h.Hosts.Addressing().PersonAddressFor(handle, name) {
		target = h.Hosts.Addressing().SiteAddressWithPath(handle, name, escRel)
	}
	if target != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target+query, http.StatusFound)
		return
	}
	if row.NamedViewers {
		h.serveViewerPage(w, r, viewerPageSignIn, "", "", "")
		return
	}
	h.servePasscodeGate(w, r, "", "", 0)
}

// gateNext is where the gate sends the visitor back to after the passcode:
// the page they asked for, on this host (never nginx's or Caddy's internal
// rewrite of it: safeNext strips that).
func gateNext(r *http.Request) string {
	p := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	return safeNext(p)
}

// safeNext keeps a path on this host: it must start with one "/" (never
// "//" or "/\", which a browser reads as another host), hold no control
// characters and never be one of the app's /internal/ pages (reachable only
// through nginx's own rewrites, so a 404 to a visitor): the passcode
// rewrite gives back the path the visitor asked for, any other /internal/
// path is "/". Anything else is "/".
func safeNext(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || len(p) > 2048 {
		return "/"
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	p = web.StripPasscodeRewrite(p)
	if strings.HasPrefix(p, "//") {
		return "/"
	}
	path, _, _ := strings.Cut(p, "?")
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	if path == "/internal" || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return "/"
	}
	return p
}

// --- Unlocking ---

// UnlockSite is POST /v1/site-unlock: the gate's form (passcode, next). It
// works only from a page on this very host, for the site this host (or, on
// a person host, next) names; a right passcode sets the unlock cookie and
// 303s back to next, a wrong one shows the gate again with the reason.
// Wrong tries are limited per site and address and per site (section
// "brute force" in passcode.go's settings). The passcode is never logged.
func (h *AccessHandler) UnlockSite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bad request")
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "the passcode is entered on the site's own page", Code: "origin_not_allowed"})
		return
	}
	host := httpx.HostName(r)
	userID, name, ok, err := h.passcodeSiteForHost(r.Context(), host, next)
	if err != nil {
		log.Printf("passcode unlock %s: %v", host, err)
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	var row model.SitePasscodeRow
	if ok {
		row, err = h.sitesSvc().GetSitePasscodeByName(r.Context(), userID, name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("passcode unlock %s: %v", host, err)
			web.RenderServiceErrorPage(w, r, h.pipeline())
			return
		}
	}
	if !ok || err != nil || row.Enc == nil {
		// Nothing to unlock here: back to the page, which shows whatever
		// it shows.
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	// The gate always posts from the site's own page, so its response is
	// never stored either.
	wait, matched, err := h.CheckVisitorPasscode(r, row, r.PostForm.Get("passcode"))
	if err != nil {
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	if wait > 0 {
		h.servePasscodeGate(w, r, next, "Too many tries. Try again in "+web.LimitSpan(roundUpMinute(wait))+".", wait)
		return
	}
	if !matched {
		h.servePasscodeGate(w, r, next, "That passcode isn’t right. Check it and try again.", 0)
		return
	}
	h.setUnlockCookie(w, r, row.SiteID, row.Generation)
	log.Printf("passcode_unlock ok=true site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func roundUpMinute(d time.Duration) time.Duration {
	if d < time.Minute {
		return time.Minute
	}
	return ((d + time.Minute - 1) / time.Minute) * time.Minute
}

// ipHash is a short, per-process salted hash of a client address for the
// log (never the address itself).
func (p *passcodeState) ipHash(ip string) string {
	p.mu.Lock()
	if p.ipSalt == nil {
		p.ipSalt = make([]byte, 16)
		_, _ = rand.Read(p.ipSalt)
	}
	salt := p.ipSalt
	p.mu.Unlock()
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil)[:6])
}

// lockedFor reports how long unlocking is refused: the site as a whole (its
// wrong tries from everyone used up RATE_LIMIT_PASSCODE_SITE) or this
// address on it (RATE_LIMIT_PASSCODE_IP). 0 = not refused.
func (p *passcodeState) lockedFor(siteID, ipKey string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	return max(p.locked["site:"+siteID].Sub(now), p.locked["ip:"+ipKey].Sub(now), 0)
}

// wrongTry counts a wrong passcode against the address and the site and
// returns the lockout it started (0 if none).
func (p *passcodeState) wrongTry(siteID, ipKey string) time.Duration {
	lim := p.Limits
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ipTries == nil {
		p.ipTries = httpx.NewRateLimiter(float64(lim.RatePasscodeIP.Burst), lim.RatePasscodeIP.PerSecond())
		p.siteTries = httpx.NewRateLimiter(float64(lim.RatePasscodeSite.Burst), lim.RatePasscodeSite.PerSecond())
		p.ipTries.Now, p.siteTries.Now = p.clock, p.clock
		p.locked = map[string]time.Time{}
	}
	now := p.clock()
	if p.ipTries.BucketCount() >= 4096 {
		p.sweep(now)
	}
	var wait time.Duration
	// The try that empties a bucket starts its lockout.
	if _, left, _ := p.ipTries.AllowState(ipKey); left < 1 {
		p.locked["ip:"+ipKey] = now.Add(lim.PasscodeLockout)
		wait = lim.PasscodeLockout
	}
	if _, left, _ := p.siteTries.AllowState(siteID); left < 1 {
		p.locked["site:"+siteID] = now.Add(lim.PasscodeSiteLockout)
		log.Printf("passcode_site_lockout site_id=%s minutes=%d", siteID, int(lim.PasscodeSiteLockout.Minutes()))
		wait = max(wait, lim.PasscodeSiteLockout)
	}
	return wait
}

// evict drops refilled buckets and ended lockouts, so a burst of wrong tries
// from many addresses does not stay in memory after it ends.
func (p *passcodeState) evict() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sweep(p.clock())
}

// sweep is evict with p.mu held.
func (p *passcodeState) sweep(now time.Time) int {
	if p.ipTries == nil {
		return 0
	}
	removed := p.ipTries.EvictIdle(0) + p.siteTries.EvictIdle(0)
	for k, until := range p.locked {
		if !now.Before(until) {
			delete(p.locked, k)
			removed++
		}
	}
	return removed
}

// --- The saved-data API ---

// passcodeGate wraps a page-facing route under /v1/sites/{sitename}/ or
// /v1/u/{handle}/sites/{sitename}/. On a protected site it lets through the
// owner's or the admin's key (or connector token), a valid unlock cookie
// for this host, and a read from a preview page whose token holds; anything
// else is 403 site_locked. Preflights, unknown sites and unprotected sites
// pass untouched (the route's own checks still run). The unlock cookie is
// never an owner power: it only gets a visitor as far as the route lets any
// visitor go.
func (h *AccessHandler) PasscodeGate(next http.Handler) http.Handler {
	return h.siteDataGate(next, true)
}

// signInGate is passcodeGate for the visitor sign-in routes (me, visitor/auth,
// verify): the passcode still applies, named viewers do not, since signing in
// is how a named viewer gets in (viewers.go).
func (h *AccessHandler) signInGate(next http.Handler) http.Handler {
	return h.siteDataGate(next, false)
}

func (h *AccessHandler) siteDataGate(next http.Handler, viewers bool) http.Handler {
	kind := accounts.VisitData
	if !viewers {
		kind = accounts.VisitSignIn
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// The routes resolve a bare name two ways (resolveSiteID, and
		// resolveWriteSiteID, which prefers the caller's own same-named
		// site): both sites must let the request in.
		siteName := strings.TrimSpace(r.PathValue("sitename"))
		ids := map[string]bool{}
		if id, _, err := h.resolveSiteIDBare(r, siteName); err == nil {
			ids[id] = true
		}
		if id, err := h.resolveWriteSiteID(r, siteName); err == nil {
			ids[id] = true
		}
		allowed, private, gated := true, false, false
		for id := range ids {
			v := h.decideData(r, id, kind)
			switch v.verdict {
			case accounts.VisitServe:
			case accounts.VisitServiceError:
				httpx.WriteInternal(w)
				return
			case accounts.VisitNeedSignIn, accounts.VisitNotListed:
				allowed, private = false, true
			default:
				allowed = false
			}
			gated = gated || v.gated
		}
		if allowed {
			if gated {
				// A protected or named-viewers site's data is never stored by
				// any cache.
				w.Header().Set("Cache-Control", "private, no-store")
			}
			next.ServeHTTP(w, r)
			return
		}
		if private {
			writeSitePrivate(w)
			return
		}
		writeSiteLocked(w)
	})
}

func writeSiteLocked(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
		Error: "this site asks for a passcode; a visitor enters it on the site's page first, and the owner's key or connector works without it",
		Code:  "site_locked",
	})
}

// PasscodeLetsIn reports whether a request may act on site siteID as a
// visitor by the passcode rule (accounts.PasscodeAdmits for the API): the
// site has no passcode, or the request passes it. Errors count as no.
func (h *AccessHandler) PasscodeLetsIn(r *http.Request, siteID string) bool {
	return h.decideData(r, siteID, accounts.VisitSignIn).verdict == accounts.VisitServe
}

// dataVisit is the visitor gate's answer for one site's data request, with
// the protection row it was decided on.
type dataVisit struct {
	verdict accounts.VisitVerdict
	gated   bool // the site has a passcode or named viewers
	row     model.SitePasscodeRow
	who     accounts.Visitor
}

// decideData loads site siteID's protection row and asks accounts.DecideVisit
// for an API request of the given kind.
func (h *AccessHandler) decideData(r *http.Request, siteID string, kind accounts.VisitKind) dataVisit {
	v := accounts.Visit{Kind: kind, Protected: true}
	v.Row, v.RowErr = h.sitesSvc().GetSitePasscode(r.Context(), siteID)
	gated := v.RowErr == nil && (v.Row.Enc != nil || v.Row.NamedViewers)
	if gated {
		v.Who, _, _ = h.visitorFacts(r, v.Row, kind, true)
	}
	verdict := accounts.DecideVisit(v)
	if verdict == accounts.VisitServe && v.Who.AdminKey && !v.Who.OwnerKey {
		log.Printf("site_gate_admin_bypass %s %s site_id=%s", r.Method, r.Pattern, siteID)
	}
	return dataVisit{verdict: verdict, gated: gated, row: v.Row, who: v.Who}
}

// KeyActsFor: the request carries the key (or connector token) of someone who
// acts as the site's owner (accounts.ActsAsOwner), or of the admin.
func (h *AccessHandler) KeyActsFor(r *http.Request, row model.SitePasscodeRow) (owner, admin bool) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		return false, false
	}
	u, ok, err := h.resolveWriterKey(r.Context(), key)
	if err != nil || !ok {
		return false, false
	}
	owner, err = accounts.ActsAsOwner(r.Context(), h.Database, u.ID, row.UserID, row.Name)
	return err == nil && owner, u.IsAdmin
}

// previewRefererFor: the request comes from a preview page of this site on
// this host whose token still holds (a preview reads the site's data the
// way the live site would).
func (h *AccessHandler) previewRefererFor(r *http.Request, siteID string) bool {
	if !web.PreviewReferer(r) {
		return false
	}
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(p, web.PreviewPathSegment+"/") {
		_, p, _ = strings.Cut(p, "/")
	}
	p = strings.TrimPrefix(p, web.PreviewPathSegment+"/")
	nStr, after, _ := strings.Cut(p, "/")
	token, _, _ := strings.Cut(after, "/")
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return false
	}
	id, v, err := web.OpenPreviewToken(h.ExportKey, h.Limits.PreviewLinkTTL, token, time.Now())
	return err == nil && id == siteID && v == n
}

// --- Owner API ---

// CheckVisitorPasscode is shared by the browser unlock and visitor tools.
// AI calls use a person bucket and a separate site budget. Browsers use IPs.
func (h *AccessHandler) CheckVisitorPasscode(r *http.Request, row model.SitePasscodeRow, supplied string) (time.Duration, bool, error) {
	siteBucket := row.SiteID
	ipKey := row.SiteID + "|" + httpx.ClientIP(r)
	if pass, ok := h.StoreAccounts().LookupInternalVisitorKey(r.Header.Get(VisitorPassHeader)); ok && pass.SiteID == row.SiteID {
		siteBucket = "ai:" + row.SiteID
		ipKey = siteBucket + "|" + pass.UserID
	}
	if wait := h.Passcode.lockedFor(siteBucket, ipKey); wait > 0 {
		log.Printf("passcode_unlock ok=false reason=locked_out site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
		return wait, false, nil
	}
	code, err := h.Passcode.open(row.SiteID, row.Enc)
	if err != nil {
		log.Printf("passcode unlock site_id=%s: %v", row.SiteID, err)
		return 0, false, err
	}
	if !passcodeMatches(strings.TrimSpace(supplied), code) {
		wait := h.Passcode.wrongTry(siteBucket, ipKey)
		log.Printf("passcode_unlock ok=false site_id=%s ip_hash=%s", row.SiteID, h.Passcode.ipHash(httpx.ClientIP(r)))
		return wait, false, nil
	}
	return 0, true, nil
}

func setOwnerFrameHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
	}
}
