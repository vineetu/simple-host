package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Named viewers: who can open a site (owner decision 2026-10-10; INTENT.md).
//
// A site's access is "anyone" (the default: anyone with the address, or with
// the passcode when the site has one) or "specific": only its owner and the
// people it names, by email, who sign in with visitor sign-in (an emailed
// code or Google) on the site's own address. The words follow Simple Host
// Enterprise ("named viewers", "who can open the site", level `specific`);
// hosted names people by the email they sign in with, not by a company
// account.
//
// It is the passcode gate's twin and uses its machinery: the same `passcode`
// marker file next to `current` (so nginx and Caddy hand every disk-served
// address to /internal/passcode), the same siteGate on every Go path to a
// site's files, the same redirect off shared hosts, and the same data-API
// wrapper. A site has a passcode or named viewers, never both (409 either
// way); the gate would ask for both if the database ever said so.
//
// Every request is checked against the list as it is now: adding someone lets
// them in on their next page, removing someone locks them out on their next
// request. Nothing is cached in a cookie but the visitor session itself,
// which is bound to one site and one host.

const (
	accessAnyone   = "anyone"
	accessSpecific = "specific"
)

// normalizeViewerEmail is the one form an email is stored and compared in:
// trimmed, lower case, plain ASCII (validEmail, the same rule as sign-in, so
// the address a visitor proves by code is exactly the one on the list). No
// provider-specific folding (dots, +tags): only the exact address counts.
func normalizeViewerEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || !validEmail.MatchString(s) || strings.Contains(s, "..") {
		return "", false
	}
	return s, true
}

// viewerSession is the visitor signed in to siteID on this host: the
// __Host- cookie (never the plain one a sibling host could plant) naming a
// live session for this site and host. Unlike strictVisitorSession it also
// counts on a navigation from elsewhere (a link in an email): the cookie is
// SameSite=Lax and host-only, and siteGate refuses cross-site subresources.
func (h *SiteHandler) viewerSession(r *http.Request, siteID string) (db.VisitorSession, bool) {
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return db.VisitorSession{}, false
	}
	sess, err := db.GetVisitorSession(r.Context(), h.database, id)
	if err != nil || !h.sessionValidFor(r, sess, siteID) {
		return db.VisitorSession{}, false
	}
	return sess, true
}

// viewerAllowed reports whether account userID may open the site: its owner,
// or a named viewer by the address the account signs in with (visitorEmail,
// the address other people's sites see). A suspended account never is. email
// is that address ("" for the owner).
func (h *SiteHandler) viewerAllowed(r *http.Request, row db.SitePasscodeRow, userID string) (ok bool, email string, err error) {
	if susp, err := db.UserSuspended(r.Context(), h.database, userID); err != nil || susp {
		return false, "", err
	}
	if userID == row.UserID {
		return true, "", nil
	}
	email, err = visitorEmail(r.Context(), h.database, userID)
	if err != nil {
		return false, "", err
	}
	norm, valid := normalizeViewerEmail(email)
	if !valid {
		return false, email, nil
	}
	listed, err := db.IsSiteViewer(r.Context(), h.database, row.SiteID, norm)
	return listed, email, err
}

// viewerGate answers a named-viewers site's file request for someone who may
// not open it, and reports whether it did. Signed out: the sign-in page (401).
// Signed in but not on the list: the private page (403). A cross-site
// subresource never gets in. The caller has set the private-cache headers.
func (h *SiteHandler) viewerGate(w http.ResponseWriter, r *http.Request, row db.SitePasscodeRow) bool {
	if crossSiteSubresource(r) {
		serveViewerPage(w, r, viewerPageSignIn, "", "", "")
		return true
	}
	sess, ok := h.viewerSession(r, row.SiteID)
	if !ok {
		serveViewerPage(w, r, viewerPageSignIn, gateNext(r), "", "")
		return true
	}
	allowed, email, err := h.viewerAllowed(r, row, sess.UserID)
	if err != nil {
		log.Printf("viewers: site %s: %v", row.SiteID, err)
		h.renderServiceError(w, r)
		return true
	}
	if !allowed {
		serveViewerPage(w, r, viewerPagePrivate, gateNext(r), email, "")
		return true
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
		_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	}
	return false
}

// viewersLetIn reports whether a data request may reach a named-viewers site
// as a visitor: the owner's or admin's key (passcodeAllows checks those
// first), a read from a preview page, or a same-origin request from the
// owner or a named viewer signed in on this host. Not a named-viewers site:
// true.
func (h *SiteHandler) viewersLetIn(r *http.Request, row db.SitePasscodeRow) (bool, error) {
	if !row.NamedViewers {
		return true, nil
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.previewRefererFor(r, row.SiteID) {
		return true, nil
	}
	sess, ok := h.strictVisitorSession(r, row.SiteID)
	if !ok {
		return false, nil
	}
	allowed, _, err := h.viewerAllowed(r, row, sess.UserID)
	return allowed, err
}

// ViewersLetIn is viewersLetIn for a site id, for storage (site_storage.go):
// errors count as no.
func (h *SiteHandler) ViewersLetIn(r *http.Request, siteID string) bool {
	row, err := db.GetSitePasscode(r.Context(), h.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		return false
	}
	ok, err := h.viewersLetIn(r, row)
	return err == nil && ok
}

func writeSitePrivate(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "this site is open only to its owner and named viewers; a visitor signs in on the site's page first, and the owner's key or connector works without it",
		Code:  "site_private",
	})
}

// --- The pages ---

type viewerPage int

const (
	viewerPageSignIn  viewerPage = iota // signed out: the email box
	viewerPageCode                      // a code was sent: the code box
	viewerPagePrivate                   // signed in, not on the list
)

var viewerGateHead = strings.Replace(strings.Replace(passcodeGateHead, "<title>Protected site</title>", "<title>Private site</title>", 1),
	`content="Protected site"`, `content="Private site"`, 1)

// serveViewerPage writes one of the named-viewers pages: never stored, never
// indexed, nothing of the site in it but the host already in the address
// bar. next "" leaves the forms out (a shared host, or a subresource). Like
// the passcode gate, a request that is not a page gets a short plain answer
// and /robots.txt says "Disallow: /".
func serveViewerPage(w http.ResponseWriter, r *http.Request, page viewerPage, next, email, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Del("Vary")
	w.Header().Del("Last-Modified")
	w.Header().Del("ETag")
	if r.URL.Path == "/robots.txt" && msg == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
		}
		return
	}
	status := http.StatusUnauthorized
	if page == viewerPagePrivate {
		status = http.StatusForbidden
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("This site is private.\n"))
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passcodeGateCSP)
	if !hackChrome {
		w.Header().Set("Content-Security-Policy", hostPasscodeGateCSP)
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(viewerPageHTML(r, page, next, email, msg)))
}

func viewerPageHTML(r *http.Request, page viewerPage, next, email, msg string) string {
	host := requestHostName(r)
	var b strings.Builder
	if hackChrome {
		b.WriteString(viewerGateHead)
	} else {
		b.Write(hostedStatusPage(viewerGateHead))
	}
	b.WriteString(`<body><main><h1>This site is private</h1>`)
	errLine := ""
	if msg != "" {
		errLine = `<p class="err" role="alert">` + html.EscapeString(msg) + `</p>`
	}
	hidden := `<input type="hidden" name="next" value="` + html.EscapeString(next) + `">`
	switch {
	case next == "":
		b.WriteString(`<p>Open it at its own address to sign in.</p>`)
	case page == viewerPagePrivate:
		b.WriteString(`<p>You are signed in as <strong>` + html.EscapeString(email) + `</strong>. Only people the owner named can open this site. Ask the owner to add you, or sign in with another email.</p>`)
		b.WriteString(errLine)
		b.WriteString(`<form method="post" action="/v1/site-signout">` + hidden)
		b.WriteString(`<button type="submit" name="then" value="switch">Switch account</button>`)
		b.WriteString(`<button type="submit" name="then" value="out" class="alt">Sign out</button></form>`)
	case page == viewerPageCode:
		b.WriteString(`<p>We sent a sign-in code to <strong>` + html.EscapeString(email) + `</strong>. Enter it below.</p>`)
		b.WriteString(errLine)
		b.WriteString(`<form method="post" action="/v1/site-signin">` + hidden)
		b.WriteString(`<input type="hidden" name="email" value="` + html.EscapeString(email) + `">`)
		b.WriteString(`<input name="code" aria-label="Sign-in code" inputmode="numeric" autocomplete="one-time-code" autofocus required maxlength="32">`)
		b.WriteString(`<button type="submit">Sign in</button></form>`)
		b.WriteString(`<form method="post" action="/v1/site-signin">` + hidden + `<input type="hidden" name="email" value="` + html.EscapeString(email) + `"><button type="submit" class="alt">Send a new code</button></form>`)
		b.WriteString(`<form method="post" action="/v1/site-signout">` + hidden + `<button type="submit" name="then" value="switch" class="alt">Use another email</button></form>`)
	default:
		b.WriteString(`<p>Only people the owner named can open it. Sign in with your email to continue.</p>`)
		b.WriteString(errLine)
		b.WriteString(`<form method="post" action="/v1/site-signin">` + hidden)
		b.WriteString(`<input name="email" type="email" aria-label="Email" placeholder="you@example.com" autocomplete="email" autofocus required maxlength="254">`)
		b.WriteString(`<button type="submit">Email me a code</button></form>`)
		if viewerGoogle && strings.HasPrefix(next, "/") {
			scheme := "http://"
			if requestIsHTTPS(r) {
				scheme = "https://"
			}
			start := "/v1/visitor/oauth/google?return_to=" + url.QueryEscape(scheme+host+next)
			b.WriteString(`<p class="or">or</p><a class="btn alt" href="` + html.EscapeString(start) + `">Continue with Google</a>`)
		}
	}
	b.WriteString(`<p class="host">` + html.EscapeString(host) + `</p></main></body></html>
`)
	return b.String()
}

// viewerGoogle: Google sign-in is set up on this install (SetVisitorSignIn),
// so the sign-in page offers it next to the emailed code.
var viewerGoogle bool

// --- Signing in and out from the pages ---

// viewerSiteForForm finds the named-viewers site a form on this host is
// for (as the passcode form does). ok=false: nothing to sign in to here.
func (h *SiteHandler) viewerSiteForForm(r *http.Request, next string) (db.SitePasscodeRow, bool, error) {
	userID, name, ok, err := h.passcodeSiteForHost(r.Context(), requestHostName(r), next)
	if err != nil || !ok {
		return db.SitePasscodeRow{}, false, err
	}
	row, err := db.GetSitePasscodeByName(r.Context(), h.database, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	if !row.NamedViewers || row.Offline || h.disk.IsSuspended(row.UserID, row.Name) {
		return row, false, nil
	}
	return row, true, nil
}

// viewerSignIn is POST /v1/site-signin, the sign-in page's form: email alone
// sends a code (the code page follows); email and code sign the visitor in to
// this site on this host and 303 back to next. Only from a page on this very
// host (login CSRF), only for the site this host names. Everyone gets a code,
// listed or not, so the form never says who is on the list.
func (h *SiteHandler) viewerSignIn(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !sameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "sign in on the site's own page", Code: "origin_not_allowed"})
		return
	}
	row, ok, err := h.viewerSiteForForm(r, next)
	if err != nil {
		log.Printf("viewers sign-in %s: %v", requestHostName(r), err)
		h.renderServiceError(w, r)
		return
	}
	if !ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if !h.visitorAuthLimiter.allow(clientIP(r)) {
		serveViewerPage(w, r, viewerPageSignIn, next, "", "Too many tries. Wait a minute and try again.")
		return
	}
	address := r.PostForm.Get("email")
	code := r.PostForm.Get("code")
	siteID := sql.NullString{String: row.SiteID, Valid: true}
	if strings.TrimSpace(code) == "" {
		if h.noEmail {
			serveViewerPage(w, r, viewerPageSignIn, next, "", "This server cannot send sign-in codes. Use Google if it is offered, or ask the site's owner.")
			return
		}
		sent, _, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.emailLimiter, address, "", "visitor", siteID, sql.NullString{}, clientIP(r), h.geoBlock)
		if status != 0 {
			msg := "Enter a valid email address."
			switch {
			case status == http.StatusTooManyRequests:
				msg = "Too many codes for that address. Wait a minute and try again."
			case status != http.StatusBadRequest:
				msg = body.Error
			}
			serveViewerPage(w, r, viewerPageSignIn, next, "", msg)
			return
		}
		serveViewerPage(w, r, viewerPageCode, next, sent, "")
		return
	}
	user, created, status, _ := verifyEmailCode(r.Context(), h.database, h.emailLimiter, verifyRequest{Email: address, Code: code}, "visitor", siteID)
	if status != 0 {
		norm := strings.ToLower(strings.TrimSpace(address))
		msg := "That code isn’t right, or it has expired. Check it, or send a new one."
		if status == http.StatusTooManyRequests {
			msg = "Too many tries. Wait a minute and send a new code."
		}
		serveViewerPage(w, r, viewerPageCode, next, norm, msg)
		return
	}
	if created {
		recordSignup(r.Context(), h.database, user.ID, db.Signup{Source: db.SignupVisitor, Method: db.SignupMethodEmail})
	}
	id, err := randomRaw(32)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}
	now := time.Now()
	if err := db.InsertVisitorSession(r.Context(), h.database, id, user.ID, row.SiteID, requestHostName(r), now.Add(visitorSessionTTL()), now.Add(visitorSessionIdle())); err != nil {
		h.renderServiceError(w, r)
		return
	}
	setVisitorSessionCookie(w, r, hex.EncodeToString(id), visitorCookieMaxAge())
	log.Printf("viewers_sign_in site_id=%s user_id=%s", row.SiteID, user.ID)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// viewerSignOut is POST /v1/site-signout, the private page's buttons: ends
// this host's visitor session and 303s back to next, which then shows the
// sign-in page. Only from a page on this very host.
func (h *SiteHandler) viewerSignOut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !sameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "sign out on the site's own page", Code: "origin_not_allowed"})
		return
	}
	if raw := strictVisitorCookie(r); raw != "" {
		if id, err := hex.DecodeString(raw); err == nil && len(id) == 32 {
			if err := db.DeleteVisitorSession(r.Context(), h.database, id); err != nil {
				h.renderServiceError(w, r)
				return
			}
		}
	}
	setVisitorSessionCookie(w, r, "", 0)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// --- Owner API ---

// siteAccessResponse is what the owner routes answer.
type siteAccessResponse struct {
	Site              string          `json:"site"`
	Access            string          `json:"access"`
	Viewers           []db.SiteViewer `json:"viewers"`
	ViewerLimit       int             `json:"viewer_limit"`
	PasscodeProtected bool            `json:"passcode_protected"`
	Added             []string        `json:"added,omitempty"`
	AlreadyListed     []string        `json:"already_listed,omitempty"`
	Removed           string          `json:"removed,omitempty"`
	Note              string          `json:"note,omitempty"`
}

func (h *SiteHandler) siteAccessState(r *http.Request, site db.Site) (siteAccessResponse, error) {
	row, err := db.GetSitePasscode(r.Context(), h.database, site.ID)
	if err != nil {
		return siteAccessResponse{}, err
	}
	viewers, err := db.ListSiteViewers(r.Context(), h.database, site.ID)
	if err != nil {
		return siteAccessResponse{}, err
	}
	access := accessAnyone
	if row.NamedViewers {
		access = accessSpecific
	}
	return siteAccessResponse{Site: site.Name, Access: access, Viewers: viewers, ViewerLimit: config.Active().SiteViewersMax, PasscodeProtected: row.Enc != nil}, nil
}

func (h *SiteHandler) writeSiteAccess(w http.ResponseWriter, r *http.Request, site db.Site, resp siteAccessResponse) {
	state, err := h.siteAccessState(r, site)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	state.Added, state.AlreadyListed, state.Removed, state.Note = resp.Added, resp.AlreadyListed, resp.Removed, resp.Note
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, state)
}

// getSiteAccess is GET /v1/sites/{sitename}/access: who can open the site
// (access "anyone" or "specific") and its named viewers.
func (h *SiteHandler) getSiteAccess(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, strings.TrimSpace(r.PathValue("sitename")))
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	h.writeSiteAccess(w, r, site, siteAccessResponse{})
}

// namedViewersRefusal is why named viewers cannot be turned on for a site
// here, as the 409 to write; "" when they can.
func (h *SiteHandler) namedViewersRefusal(site db.Site, row db.SitePasscodeRow) (code, msg string) {
	switch {
	case hackMode:
		return "named_viewers_unavailable", "named viewers are not available on Simple Hack; event and team sites have their own access rules"
	case h.noVisitorSignIn:
		return "sign_in_unavailable", "named viewers sign in with an emailed code or Google, and this server has neither set up"
	case h.SharedOrigin() || contentHostOnlySites[site.OwnerHandle+"/"+site.Name] || !h.personAddressFor(site.OwnerHandle, site.Name):
		return "named_viewers_need_own_address", "named viewers need the site to have an address of its own, and this site is served on an address every site shares"
	case row.Enc != nil:
		return "passcode_set", "this site has a passcode; a site has a passcode or named viewers, not both. Remove the passcode first (set_site_passcode action remove, or DELETE /v1/sites/{sitename}/lock), after the person agrees"
	}
	return "", ""
}

// turnOnNamedViewers sets access to specific: the marker first, then the
// database, so a failure in between leaves the site closed, never open.
func (h *SiteHandler) turnOnNamedViewers(w http.ResponseWriter, r *http.Request, site db.Site, row db.SitePasscodeRow) bool {
	if row.NamedViewers {
		return true
	}
	if code, msg := h.namedViewersRefusal(site, row); code != "" {
		writeJSON(w, http.StatusConflict, errorResponse{Error: msg, Code: code})
		return false
	}
	if err := h.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil {
		log.Printf("viewers: marker for site %s: %v", site.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "the site's files could not be marked; nothing changed, try again"})
		return false
	}
	if err := db.SetSiteAccess(r.Context(), h.database, site.ID, accessSpecific); err != nil {
		_ = h.disk.SetPasscodeMarker(site.UserID, site.Name, row.Enc != nil)
		writeSiteLookupError(w, err)
		return false
	}
	log.Printf("site_access access=specific site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	return true
}

// putSiteAccess is PUT /v1/sites/{sitename}/access {"access": "anyone" |
// "specific"}. specific: only the owner and the named viewers can open the
// site (with none named yet, only the owner). anyone: open to everyone with
// the address again; the list is kept for next time.
func (h *SiteHandler) putSiteAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Access string `json:"access"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Access != accessAnyone && req.Access != accessSpecific) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `invalid JSON body (expected {"access":"anyone"} or {"access":"specific"})`, Code: "invalid_request"})
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
	row, err := db.GetSitePasscode(r.Context(), h.database, site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	note := ""
	if req.Access == accessSpecific {
		if !h.turnOnNamedViewers(w, r, site, row) {
			return
		}
		note = "Only you and the named viewers can open the site now; everyone else sees a sign-in page. Viewers sign in on the site with the email you named."
	} else {
		if row.NamedViewers {
			// The database first, then the marker (kept while a passcode
			// remains): a failure in between leaves the site closed.
			if err := db.SetSiteAccess(r.Context(), h.database, site.ID, accessAnyone); err != nil {
				writeSiteLookupError(w, err)
				return
			}
			if row.Enc == nil {
				if err := h.disk.SetPasscodeMarker(site.UserID, site.Name, false); err != nil {
					log.Printf("viewers: marker for site %s: %v", site.ID, err)
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "the site is open again, but its files could not be updated; try again"})
					return
				}
			}
			log.Printf("site_access access=anyone site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
		}
		note = "Anyone with the address can open the site. The named viewers are kept, in case you turn it back on."
	}
	h.writeSiteAccess(w, r, site, siteAccessResponse{Note: note})
}

// addSiteViewers is POST /v1/sites/{sitename}/viewers {"emails": [...]}: names
// viewers and sets access to specific (one call: "only mom and dad").
func (h *SiteHandler) addSiteViewers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Emails []string `json:"emails"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || len(req.Emails) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `invalid JSON body (expected {"emails":["person@example.com"]})`, Code: "invalid_request"})
		return
	}
	limit := config.Active().SiteViewersMax
	var emails []string
	seen := map[string]bool{}
	for _, raw := range req.Emails {
		e, ok := normalizeViewerEmail(raw)
		if !ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "not an email address: " + strings.TrimSpace(raw), Code: "invalid_email"})
			return
		}
		if !seen[e] {
			seen[e] = true
			emails = append(emails, e)
		}
	}
	if len(emails) > limit {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a site can name at most " + strconv.Itoa(limit) + " viewers", Code: "too_many_viewers"})
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
	row, err := db.GetSitePasscode(r.Context(), h.database, site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if !row.NamedViewers {
		if code, msg := h.namedViewersRefusal(site, row); code != "" {
			writeJSON(w, http.StatusConflict, errorResponse{Error: msg, Code: code})
			return
		}
	}
	have, err := db.CountSiteViewers(r.Context(), h.database, site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	var fresh []string
	var already []string
	for _, e := range emails {
		if listed, err := db.IsSiteViewer(r.Context(), h.database, site.ID, e); err != nil {
			writeSiteLookupError(w, err)
			return
		} else if listed {
			already = append(already, e)
		} else {
			fresh = append(fresh, e)
		}
	}
	if have+len(fresh) > limit {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "a site can name at most " + strconv.Itoa(limit) + " viewers; it has " + strconv.Itoa(have) + ". Remove some first", Code: "too_many_viewers"})
		return
	}
	// The list first, then the gate: a failure in between leaves the site as
	// it was, with the names ready.
	for _, e := range fresh {
		if _, err := db.AddSiteViewer(r.Context(), h.database, site.ID, e); err != nil {
			writeSiteLookupError(w, err)
			return
		}
	}
	if !h.turnOnNamedViewers(w, r, site, row) {
		return
	}
	log.Printf("site_viewers added=%d site_id=%s name=%s by=%s", len(fresh), site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "Only you and the named viewers can open the site. Tell them to open its address and sign in with the email you named (an emailed code, or Google for a Google address)."
	h.writeSiteAccess(w, r, site, siteAccessResponse{Added: fresh, AlreadyListed: already, Note: note})
}

// removeSiteViewer is DELETE /v1/sites/{sitename}/viewers/{email}: takes one
// person off the list; their next request is refused. Access stays as it is:
// removing the last viewer leaves the site open only to its owner.
func (h *SiteHandler) removeSiteViewer(w http.ResponseWriter, r *http.Request) {
	e, ok := normalizeViewerEmail(r.PathValue("email"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "not an email address", Code: "invalid_email"})
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	removed, err := db.RemoveSiteViewer(r.Context(), h.database, site.ID, e)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: e + " is not a named viewer of this site", Code: "viewer_not_found"})
		return
	}
	log.Printf("site_viewers removed=1 site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := e + " can no longer open the site."
	h.writeSiteAccess(w, r, site, siteAccessResponse{Removed: e, Note: note})
}

// siteAccessOf is a site's access as the site routes report it.
func siteAccessOf(s db.Site) string {
	if s.NamedViewers {
		return accessSpecific
	}
	return accessAnyone
}
