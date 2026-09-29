package handler

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Site passcodes (owner decision 2026-09-29; INTENT.md).
//
// An owner may put one passcode on a whole site. Every address of the site
// then answers a plain "This site is protected" page (401, noindex, nothing
// of the site in it) until the visitor enters the passcode; after that an
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
	aead   cipher.AEAD // nil: PASSCODE_ENC_KEY unset
	macKey []byte

	mu        sync.Mutex
	ipTries   map[string]*passcodeBucket // site id + "|" + client address
	siteTries map[string]*passcodeBucket // site id
	now       func() time.Time
	// ipSalt makes the logged address hash unlinkable across restarts.
	ipSalt []byte
}

type passcodeBucket struct {
	tokens      float64
	last        time.Time
	lockedUntil time.Time
}

// SetPasscodeKey reads PASSCODE_ENC_KEY (32 bytes, standard base64). Empty
// leaves passcodes unavailable; anything else malformed is an error, so a
// typo stops startup instead of silently turning passcodes off.
func (h *SiteHandler) SetPasscodeKey(b64 string) error {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		h.passcode.aead, h.passcode.macKey = nil, nil
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != 32 {
		return errors.New("PASSCODE_ENC_KEY must be 32 random bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("simple-host passcode unlock cookie v1"))
	h.passcode.aead, h.passcode.macKey = aead, mac.Sum(nil)
	return nil
}

// PasscodesReady reports whether a passcode can be set on this server:
// SITE_PASSCODES is on, the key is set and sites have addresses of their own.
func (h *SiteHandler) PasscodesReady() bool {
	return config.Active().SitePasscodes && h.passcode.aead != nil && !h.SharedOrigin()
}

func (p *passcodeState) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// seal encrypts a passcode for one site.
func (p *passcodeState) seal(siteID, code string) ([]byte, error) {
	if p.aead == nil {
		return nil, errPasscodeNoKey
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.aead.Seal(nonce, nonce, []byte(code), []byte("site:"+siteID)), nil
}

// open decrypts a site's sealed passcode.
func (p *passcodeState) open(siteID string, enc []byte) (string, error) {
	if p.aead == nil {
		return "", errPasscodeNoKey
	}
	n := p.aead.NonceSize()
	if len(enc) < n+p.aead.Overhead() {
		return "", errPasscodeSealed
	}
	pt, err := p.aead.Open(nil, enc[:n], enc[n:], []byte("site:"+siteID))
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
// There are no other rules: any characters, digits only included (owner
// decision 2026-09-29). Control characters are refused because the gate's
// one-line box could never send them.
func normalizePasscode(code string) (string, error) {
	code = strings.TrimSpace(code)
	min := config.Active().PasscodeMinLength
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
func generatePasscode() (string, error) {
	n := config.Active().PasscodeMinLength
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
	if requestIsHTTPS(r) {
		return "__Host-" + name
	}
	return name
}

func (p *passcodeState) cookieMAC(siteID, host string, exp int64, gen int) string {
	m := hmac.New(sha256.New, p.macKey)
	fmt.Fprintf(m, "v1|%s|%s|%d|%d", siteID, strings.ToLower(host), exp, gen)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setUnlockCookie lets this browser into the site on this host.
func (h *SiteHandler) setUnlockCookie(w http.ResponseWriter, r *http.Request, siteID string, gen int) {
	exp := h.passcode.clock().Add(passcodeCookieMaxAge).Unix()
	value := "v1." + strconv.FormatInt(exp, 10) + "." + strconv.Itoa(gen) + "." + h.passcode.cookieMAC(siteID, requestHostName(r), exp, gen)
	http.SetCookie(w, &http.Cookie{
		Name:     passcodeCookieName(r, siteID),
		Value:    value,
		Path:     "/",
		MaxAge:   int(passcodeCookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// unlocked reports whether the request carries a valid unlock cookie for
// this site, on this host, for the passcode's current generation.
func (h *SiteHandler) unlocked(r *http.Request, siteID string, gen int) bool {
	if h.passcode.macKey == nil {
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
	if err1 != nil || err2 != nil || g != gen || exp < h.passcode.clock().Unix() {
		return false
	}
	want := h.passcode.cookieMAC(siteID, requestHostName(r), exp, g)
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

// siteGate answers the take-down, offline or passcode page for a site when
// one applies, and reports whether it did. Every Go path to a site's files
// goes through it (serveSiteFile); a passcode-protected site that lets the
// request in gets its private-cache headers here.
func (h *SiteHandler) siteGate(w http.ResponseWriter, r *http.Request, userID, siteName, rel string) bool {
	// A site the operator has taken down answers the take-down page on
	// every path, whichever address reached it (suspend.go).
	if h.disk.IsSuspended(userID, siteName) {
		serveTakedown(w, r)
		return true
	}
	// Taken offline by its owner (offline.go): the same on every address.
	if h.disk.IsOffline(userID, siteName) {
		serveOffline(w, r)
		return true
	}
	if !h.disk.HasPasscodeMarker(userID, siteName) {
		return false
	}
	row, err := db.GetSitePasscodeByName(r.Context(), h.database, userID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false // no such live site: the caller's 404 stands
		}
		// Fail closed: the marker says protected and the database cannot
		// say otherwise.
		log.Printf("passcode: site %s/%s: %v", userID, siteName, err)
		h.renderServiceError(w, r)
		return true
	}
	if row.Enc == nil {
		return false // unlocked in the database; the marker is on its way out
	}
	// Nothing of a protected site is ever stored by a shared cache, served
	// to another site's page, or indexed.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Add("Vary", "Cookie")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if h.sharedHost(requestHostName(r)) {
		h.passcodeElsewhere(w, r, row, rel)
		return true
	}
	if h.unlocked(r, row.SiteID, row.Generation) && !crossSiteSubresource(r) {
		return false
	}
	servePasscodeGate(w, r, gateNext(r), "", 0)
	return true
}

// sharedHost: a host every site shares (the content host, the apex). A
// passcode is never entered there: the cookie would be every site's.
func (h *SiteHandler) sharedHost(host string) bool {
	return (h.contentHost != "" && strings.EqualFold(host, h.contentHost)) || h.isVisitorApexHost(host)
}

// passcodeElsewhere answers a protected site's file on a shared host: a 302
// to the same path on the site's own address, or, when it has none, the
// plain protected page without a form.
func (h *SiteHandler) passcodeElsewhere(w http.ResponseWriter, r *http.Request, row db.SitePasscodeRow, rel string) {
	escRel := "/" + strings.TrimLeft((&url.URL{Path: rel}).EscapedPath(), "/")
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	target := ""
	if info, has, err := h.siteOwnAddress(r.Context(), row.SiteID); err == nil && has {
		target = "https://" + strings.ToLower(info.Domain) + escRel
	} else if handle, _, name, err := db.GetSiteOwner(r.Context(), h.database, row.SiteID); err == nil && handle != "" && h.personAddressFor(handle, name) {
		target = h.siteAddressWithPath(handle, name, escRel)
	}
	if target != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target+query, http.StatusFound)
		return
	}
	servePasscodeGate(w, r, "", "", 0)
}

// gateNext is where the gate sends the visitor back to after the passcode:
// the page they asked for, on this host.
func gateNext(r *http.Request) string {
	p := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	return safeNext(p)
}

// safeNext keeps a path on this host: it must start with one "/" (never
// "//" or "/\", which a browser reads as another host) and hold no control
// characters. Anything else is "/".
func safeNext(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || len(p) > 2048 {
		return "/"
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	return p
}

// passcodeGateCSS is the gate's only style; its hash is in the page's CSP.
const passcodeGateCSS = `body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 16px;text-align:center}main{max-width:360px;margin:0 auto}h1{font-size:24px;margin:0 0 8px}p{color:#5b6576;margin:0 0 20px}form{display:flex;flex-direction:column;gap:10px}input{font:inherit;padding:12px;border:1px solid #c5ccd8;border-radius:8px;background:#fff;color:inherit;text-align:center}button{font:inherit;font-weight:600;padding:12px;border:0;border-radius:8px;background:#1a2233;color:#fff;cursor:pointer}.err{color:#b42318;margin:0 0 12px}.host{font-size:14px;margin:16px 0 0;word-break:break-all}html[data-theme=dark] body{color:#e6ebf3;background:#0b1222}html[data-theme=dark] p{color:#a3afc1}html[data-theme=dark] input{background:#131c30;border-color:#34405a}html[data-theme=dark] button{background:#e6ebf3;color:#0b1222}html[data-theme=dark] .err{color:#ff8a80}`

// passcodeGateHead is the gate's head with the site-wide theme script
// (partials/theme.html, like the offline page), built once.
var passcodeGateHead = themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow"><meta name="referrer" content="same-origin"><!--sh:theme-->
<title>Protected site</title><meta property="og:title" content="Protected site">
<style>` + passcodeGateCSS + `</style></head>`)

var passcodeGateCSP = func() string {
	css := sha256.Sum256([]byte(passcodeGateCSS))
	csp := "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(css[:]) + "'"
	if i := strings.Index(passcodeGateHead, "<script>"); i >= 0 {
		body := passcodeGateHead[i+len("<script>"):]
		body = body[:strings.Index(body, "</script>")]
		js := sha256.Sum256([]byte(body))
		csp += "; script-src 'sha256-" + base64.StdEncoding.EncodeToString(js[:]) + "'"
	}
	return csp + "; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
}()

// servePasscodeGate writes the protected page: 401, never stored, never
// indexed, nothing of the site in it (no title, no owner, no preview image;
// the only name on it is the host already in the address bar). next ""
// leaves the form out (a shared host, where no passcode is entered). A
// request that is not a page (a script, an image, a fetch) gets a short
// plain 401. /robots.txt answers "Disallow: /" with 200, so crawlers stop
// asking.
func servePasscodeGate(w http.ResponseWriter, r *http.Request, next, msg string, retryAfter time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Del("Vary")
	w.Header().Del("Last-Modified")
	w.Header().Del("ETag")
	if r.URL.Path == "/robots.txt" && retryAfter == 0 && msg == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
		}
		return
	}
	status := http.StatusUnauthorized
	if retryAfter > 0 {
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("This site is protected.\n"))
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passcodeGateCSP)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(passcodeGateHTML(requestHostName(r), next, msg)))
}

func passcodeGateHTML(host, next, msg string) string {
	var b strings.Builder
	b.WriteString(passcodeGateHead)
	b.WriteString(`<body><main><h1>This site is protected</h1>`)
	if next == "" {
		b.WriteString(`<p>Open it at its own address to enter the passcode.</p>`)
	} else {
		b.WriteString(`<p>Enter the passcode you were given.</p>`)
		if msg != "" {
			b.WriteString(`<p class="err" role="alert">` + html.EscapeString(msg) + `</p>`)
		}
		b.WriteString(`<form method="post" action="/v1/site-unlock"><input type="hidden" name="next" value="` + html.EscapeString(next) + `">`)
		b.WriteString(`<input name="passcode" type="password" aria-label="Passcode" autocomplete="current-password" autofocus required maxlength="1024">`)
		b.WriteString(`<button type="submit">Open site</button></form>`)
	}
	b.WriteString(`<p class="host">` + html.EscapeString(host) + `</p></main></body></html>
`)
	return b.String()
}

// passcodePageHandler answers /internal/passcode/{rest...}, where nginx and
// Caddy send a request for a site whose folder carries the passcode marker.
// On a custom domain, a claimed name or a family address the host names the
// site: the gate, or the file once unlocked (a family address of a site
// that lives on a domain of its own redirects there first). On the content host the path does
// (/<handle>/<site>/...): that site's own address. Anything else — a
// hand-made vhost the app does not know — gets the protected page with no
// form: closed, never the file.
func (h *SiteHandler) passcodePageHandler(w http.ResponseWriter, r *http.Request) {
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	host := requestHostName(r)
	if h.sharedHost(host) {
		handle, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		name, after, _ := strings.Cut(tail, "/")
		if handle == "" || name == "" {
			h.notFound(w, r)
			return
		}
		r.SetPathValue("handle", handle)
		r.SetPathValue("sitename", name)
		r.SetPathValue("rest", after)
		h.contentHostRedirect(w, r)
		return
	}
	info, err := db.GetSiteByCustomDomain(r.Context(), h.database, host)
	if err != nil || !info.VerifiedAt.Valid {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("passcode: domain %s: %v", host, err)
		}
		// A family address (familyhost.go): the host names the site too.
		if m, ok, ferr := h.familySiteForHost(r.Context(), host); ferr == nil && ok {
			h.serveFamilyFile(w, r, m, rest, (&url.URL{Path: rest}).EscapedPath())
			return
		} else if ferr != nil {
			log.Printf("passcode: family host %s: %v", host, ferr)
			h.renderServiceError(w, r)
			return
		}
		servePasscodeGate(w, r, "", "", 0)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	escRest := (&url.URL{Path: rest}).EscapedPath()
	h.serveSiteFile(w, r, info.UserID, info.Name, rest, escRest)
}

// --- Unlocking ---

// passcodeSiteForHost finds the protected site a visitor is unlocking: the
// one the host names (its site host, custom domain or claimed name), or on
// a person host the one the path's first segment names. Never on a shared
// host.
func (h *SiteHandler) passcodeSiteForHost(ctx context.Context, host, next string) (userID, name string, ok bool, err error) {
	if host == "" || h.sharedHost(host) {
		return "", "", false, nil
	}
	if h.isSiteHostName(host) {
		site, _, ok, err := h.siteHostSite(ctx, host)
		if err != nil || !ok {
			return "", "", false, err
		}
		return site.UserID, site.Name, true, nil
	}
	if owner, onPerson := h.personHostOwner(ctx, host); onPerson {
		seg, _, _ := strings.Cut(strings.TrimPrefix(next, "/"), "/")
		seg, _, _ = strings.Cut(seg, "?")
		if !validSiteName.MatchString(seg) {
			return "", "", false, nil
		}
		site, err := db.GetSiteByUser(ctx, h.database, owner.ID, seg)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		if err != nil {
			return "", "", false, err
		}
		return site.UserID, site.Name, true, nil
	}
	// A family address names its site (a custom domain bound under the
	// family is not one: familySiteForHost leaves it to the lookup below).
	if m, ok, err := h.familySiteForHost(ctx, host); err != nil {
		return "", "", false, err
	} else if ok {
		return m.Site.UserID, m.Site.Name, true, nil
	}
	info, err := db.GetSiteByCustomDomain(ctx, h.database, host)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if !info.VerifiedAt.Valid {
		return "", "", false, nil
	}
	return info.UserID, info.Name, true, nil
}

// unlockSite is POST /v1/site-unlock: the gate's form (passcode, next). It
// works only from a page on this very host, for the site this host (or, on
// a person host, next) names; a right passcode sets the unlock cookie and
// 303s back to next, a wrong one shows the gate again with the reason.
// Wrong tries are limited per site and address and per site (section
// "brute force" in passcode.go's settings). The passcode is never logged.
func (h *SiteHandler) unlockSite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !sameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "the passcode is entered on the site's own page", Code: "origin_not_allowed"})
		return
	}
	host := requestHostName(r)
	userID, name, ok, err := h.passcodeSiteForHost(r.Context(), host, next)
	if err != nil {
		log.Printf("passcode unlock %s: %v", host, err)
		h.renderServiceError(w, r)
		return
	}
	var row db.SitePasscodeRow
	if ok {
		row, err = db.GetSitePasscodeByName(r.Context(), h.database, userID, name)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("passcode unlock %s: %v", host, err)
			h.renderServiceError(w, r)
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
	ipKey := row.SiteID + "|" + clientIP(r)
	if wait := h.passcode.lockedFor(row.SiteID, ipKey); wait > 0 {
		log.Printf("passcode_unlock ok=false reason=locked_out site_id=%s ip_hash=%s", row.SiteID, h.passcode.ipHash(clientIP(r)))
		servePasscodeGate(w, r, next, "Too many tries. Try again in "+config.Span(roundUpMinute(wait))+".", wait)
		return
	}
	code, err := h.passcode.open(row.SiteID, row.Enc)
	if err != nil {
		log.Printf("passcode unlock site_id=%s: %v", row.SiteID, err)
		h.renderServiceError(w, r)
		return
	}
	if !passcodeMatches(strings.TrimSpace(r.PostForm.Get("passcode")), code) {
		wait := h.passcode.wrongTry(row.SiteID, ipKey)
		log.Printf("passcode_unlock ok=false site_id=%s ip_hash=%s", row.SiteID, h.passcode.ipHash(clientIP(r)))
		if wait > 0 {
			servePasscodeGate(w, r, next, "Too many tries. Try again in "+config.Span(roundUpMinute(wait))+".", wait)
			return
		}
		servePasscodeGate(w, r, next, "That passcode isn’t right. Check it and try again.", 0)
		return
	}
	h.setUnlockCookie(w, r, row.SiteID, row.Generation)
	log.Printf("passcode_unlock ok=true site_id=%s ip_hash=%s", row.SiteID, h.passcode.ipHash(clientIP(r)))
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
	var wait time.Duration
	if b := p.siteTries[siteID]; b != nil && now.Before(b.lockedUntil) {
		wait = b.lockedUntil.Sub(now)
	}
	if b := p.ipTries[ipKey]; b != nil && now.Before(b.lockedUntil) && b.lockedUntil.Sub(now) > wait {
		wait = b.lockedUntil.Sub(now)
	}
	return wait
}

// wrongTry counts a wrong passcode against the address and the site and
// returns the lockout it started (0 if none).
func (p *passcodeState) wrongTry(siteID, ipKey string) time.Duration {
	lim := config.Active()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ipTries == nil {
		p.ipTries = map[string]*passcodeBucket{}
		p.siteTries = map[string]*passcodeBucket{}
	}
	now := p.clock()
	p.sweepLocked(now)
	var wait time.Duration
	if take(p.ipTries, ipKey, lim.RatePasscodeIP, now) {
		p.ipTries[ipKey].lockedUntil = now.Add(lim.PasscodeLockout)
		wait = lim.PasscodeLockout
	}
	if take(p.siteTries, siteID, lim.RatePasscodeSite, now) {
		p.siteTries[siteID].lockedUntil = now.Add(lim.PasscodeSiteLockout)
		log.Printf("passcode_site_lockout site_id=%s minutes=%d", siteID, int(lim.PasscodeSiteLockout.Minutes()))
		if lim.PasscodeSiteLockout > wait {
			wait = lim.PasscodeSiteLockout
		}
	}
	return wait
}

// take spends one token of key's bucket and reports whether that emptied it.
func take(m map[string]*passcodeBucket, key string, rate config.Rate, now time.Time) bool {
	b := m[key]
	if b == nil {
		b = &passcodeBucket{tokens: float64(rate.Burst), last: now}
		m[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate.PerSecond()
	if b.tokens > float64(rate.Burst) {
		b.tokens = float64(rate.Burst)
	}
	b.last = now
	b.tokens--
	if b.tokens < 1 {
		b.tokens = 0
		return true
	}
	return false
}

// startSweep evicts idle counters every few minutes, so a burst of wrong
// tries from many addresses does not stay in memory after it ends.
func (p *passcodeState) startSweep(every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for range t.C {
			p.mu.Lock()
			p.sweep(p.clock())
			p.mu.Unlock()
		}
	}()
}

// sweepLocked drops buckets that have refilled and are not locked out, so
// the maps stay small under a churn of addresses. Called with p.mu held.
func (p *passcodeState) sweepLocked(now time.Time) {
	if len(p.ipTries) < 4096 {
		return
	}
	p.sweep(now)
}

// sweep drops every bucket that has refilled and is not locked out.
// Called with p.mu held.
func (p *passcodeState) sweep(now time.Time) {
	lim := config.Active()
	for _, pair := range []struct {
		m    map[string]*passcodeBucket
		rate config.Rate
	}{{p.ipTries, lim.RatePasscodeIP}, {p.siteTries, lim.RatePasscodeSite}} {
		for k, b := range pair.m {
			full := b.tokens+now.Sub(b.last).Seconds()*pair.rate.PerSecond() >= float64(pair.rate.Burst)
			if full && !now.Before(b.lockedUntil) {
				delete(pair.m, k)
			}
		}
	}
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
func (h *SiteHandler) passcodeGate(next http.Handler) http.Handler {
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
		if id, err := h.resolveSiteID(r, siteName); err == nil {
			ids[id] = true
		}
		if id, err := h.resolveWriteSiteID(r, siteName); err == nil {
			ids[id] = true
		}
		allowed := true
		for id := range ids {
			ok, err := h.passcodeAllows(r, id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			allowed = allowed && ok
		}
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		writeSiteLocked(w)
	})
}

func writeSiteLocked(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "this site asks for a passcode; a visitor enters it on the site's page first, and the owner's key or connector works without it",
		Code:  "site_locked",
	})
}

// PasscodeLetsIn reports whether a request may act on site siteID as a
// visitor: the site has no passcode, or the request passes it (see
// passcodeAllows). Errors count as no.
func (h *SiteHandler) PasscodeLetsIn(r *http.Request, siteID string) bool {
	ok, err := h.passcodeAllows(r, siteID)
	return err == nil && ok
}

// passcodeAllows reports whether a data request may reach site siteID: it
// has no passcode, or the request carries the owner's or the admin's key
// (the admin's use is logged), this host's unlock cookie, or (reads only)
// comes from a preview page of this site.
func (h *SiteHandler) passcodeAllows(r *http.Request, siteID string) (bool, error) {
	row, err := db.GetSitePasscode(r.Context(), h.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if row.Enc == nil {
		return true, nil
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		if u, ok, kerr := h.resolveWriterKey(r.Context(), key); kerr == nil && ok && (u.ID == row.UserID || u.IsAdmin) {
			if u.ID != row.UserID {
				log.Printf("passcode_admin_bypass %s %s site_id=%s by=%s", r.Method, r.Pattern, row.SiteID, u.ID)
			}
			return true, nil
		}
	}
	if h.unlocked(r, row.SiteID, row.Generation) && !crossSiteSubresource(r) {
		return true, nil
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.previewRefererFor(r, row.SiteID), nil
}

// previewRefererFor: the request comes from a preview page of this site on
// this host whose token still holds (a preview reads the site's data the
// way the live site would).
func (h *SiteHandler) previewRefererFor(r *http.Request, siteID string) bool {
	if !previewReferer(r) {
		return false
	}
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(p, previewPathSegment+"/") {
		_, p, _ = strings.Cut(p, "/")
	}
	p = strings.TrimPrefix(p, previewPathSegment+"/")
	nStr, after, _ := strings.Cut(p, "/")
	token, _, _ := strings.Cut(after, "/")
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return false
	}
	id, v, err := h.checkPreviewToken(token, time.Now())
	return err == nil && id == siteID && v == n
}

// --- Owner API ---

// passcodeResponse is what the owner routes answer.
type passcodeResponse struct {
	Site              string     `json:"site"`
	PasscodeProtected bool       `json:"passcode_protected"`
	Passcode          string     `json:"passcode,omitempty"`
	PasscodeSetAt     *time.Time `json:"passcode_set_at,omitempty"`
	Note              string     `json:"note,omitempty"`
}

// ownerSiteForPasscode loads the caller's site by {sitename} under its lock
// for a change. ok=false after writing the answer.
func (h *SiteHandler) ownerSiteForPasscode(w http.ResponseWriter, r *http.Request) (db.Site, func(), bool) {
	user := auth.GetUser(r.Context())
	name := strings.TrimSpace(r.PathValue("sitename"))
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name); err != nil {
		writeSiteLookupError(w, err)
		return db.Site{}, nil, false
	}
	unlock := h.lockSite(user.ID, name)
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil {
		unlock()
		writeSiteLookupError(w, err)
		return db.Site{}, nil, false
	}
	if adminSiteMismatch(r, site.ID) {
		unlock()
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return db.Site{}, nil, false
	}
	return site, unlock, true
}

func writeSiteLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

// getSitePasscode is GET /v1/sites/{sitename}/lock: whether the site has a
// passcode, since when, and the passcode itself (owner only; the admin's
// routes never read it).
func (h *SiteHandler) getSitePasscode(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, strings.TrimSpace(r.PathValue("sitename")))
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	row, err := db.GetSitePasscode(r.Context(), h.database, site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	resp := passcodeResponse{Site: site.Name, PasscodeProtected: row.Enc != nil}
	if row.Enc != nil {
		code, err := h.passcode.open(site.ID, row.Enc)
		if err != nil {
			log.Printf("passcode read site_id=%s: %v", site.ID, err)
			resp.Note = "The passcode is set but cannot be read on this server (its key changed). Set a new one."
		} else {
			resp.Passcode = code
		}
		if row.SetAt.Valid {
			t := row.SetAt.Time.UTC()
			resp.PasscodeSetAt = &t
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// putSitePasscode is PUT /v1/sites/{sitename}/lock with {"passcode": "..."}
// or {"generate": true}: sets or changes the passcode. A change signs
// everyone out; the same passcode again changes nothing.
func (h *SiteHandler) putSitePasscode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passcode *string `json:"passcode"`
		Generate bool    `json:"generate"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Passcode == nil) == !req.Generate {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `invalid JSON body (expected {"passcode":"..."} or {"generate":true})`, Code: "invalid_request"})
		return
	}
	if !config.Active().SitePasscodes {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "site passcodes are not enabled on this server yet", Code: "passcodes_not_enabled"})
		return
	}
	if h.passcode.aead == nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "site passcodes are not enabled on this server (it has no PASSCODE_ENC_KEY)", Code: "passcodes_not_enabled"})
		return
	}
	var code string
	var err error
	if req.Generate {
		code, err = generatePasscode()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	} else if code, err = normalizePasscode(*req.Passcode); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_passcode"})
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if refuseSuspendedSite(w, site) {
		return
	}
	if h.SharedOrigin() || contentHostOnlySites[site.OwnerHandle+"/"+site.Name] || !h.personAddressFor(site.OwnerHandle, site.Name) {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "a passcode needs the site to have an address of its own, and this site is served on an address every site shares",
			Code:  "passcode_needs_own_address",
		})
		return
	}
	row, err := db.GetSitePasscode(r.Context(), h.database, site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if row.Enc != nil {
		if cur, oerr := h.passcode.open(site.ID, row.Enc); oerr == nil && cur == code {
			resp := passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, Note: "That is already this site's passcode; nothing changed."}
			if row.SetAt.Valid {
				t := row.SetAt.Time.UTC()
				resp.PasscodeSetAt = &t
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	enc, err := h.passcode.seal(site.ID, code)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// The marker first: if the database write then fails, the site is
	// shown the gate (closed) until the boot sync removes it, never open.
	if err := h.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "the site's files could not be marked; nothing changed, try again"})
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.SetSitePasscode(r.Context(), h.database, site.ID, enc, now); err != nil {
		if row.Enc == nil {
			_ = h.disk.SetPasscodeMarker(site.UserID, site.Name, false)
		}
		writeSiteLookupError(w, err)
		return
	}
	changed := row.Enc != nil
	log.Printf("site_passcode set=true changed=%t generated=%t site_id=%s name=%s by=%s", changed, req.Generate, site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "Every address of the site now asks for this passcode. Anyone you give it to can open the site and pass it on."
	if changed {
		note = "The passcode is changed: everyone who had entered the old one must enter the new one."
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, PasscodeSetAt: &now, Note: note})
}

// deleteSitePasscode is DELETE /v1/sites/{sitename}/lock: the site opens to
// everyone again. Also the admin's (moderation), through asSiteOwner.
func (h *SiteHandler) deleteSitePasscode(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	// The database first, then the marker: a failure in between leaves the
	// site closed until the boot sync, never open with the lock recorded.
	if err := db.ClearSitePasscode(r.Context(), h.database, site.ID); err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if err := h.disk.SetPasscodeMarker(site.UserID, site.Name, false); err != nil {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "the passcode is removed, but the site's files could not be updated; try again"})
		return
	}
	log.Printf("site_passcode set=false site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: false, Note: "The passcode is removed: every address of the site is open to everyone again."})
}

// signOutEveryone is POST /v1/sites/{sitename}/lock/sign-out-everyone:
// everyone who entered the passcode must enter it again.
func (h *SiteHandler) signOutEveryone(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if _, err := db.BumpSitePasscodeGeneration(r.Context(), h.database, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this site has no passcode", Code: "no_passcode"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("site_passcode sign_out_everyone site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: true, Note: "Everyone is signed out: each visitor must enter the passcode again."})
}

// syncPasscodeMarker makes the disk marker match the database.
func (h *SiteHandler) syncPasscodeMarker(s db.Site) error {
	return h.disk.SetPasscodeMarker(s.UserID, s.Name, s.Passcode)
}
