package accesshttp

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Named viewers: who can open a site.
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

// viewerSession is the visitor signed in to siteID on this host: the
// __Host- cookie (never the plain one a sibling host could plant) naming a
// live session for this site and host. Unlike StrictVisitorSession it also
// counts on a navigation from elsewhere (a link in an email): the cookie is
// SameSite=Lax and host-only, and siteGate refuses cross-site subresources.
func (h *AccessHandler) viewerSession(r *http.Request, siteID string) (model.VisitorSession, bool) {
	if r.Header.Get(VisitorPassHeader) != "" {
		return h.InternalVisitorSession(r, siteID)
	}
	sess, err := h.cookieSession(r, siteID)
	return sess, err == nil
}

// viewerAllowed reports whether account userID may open the site
// (accounts.ViewerAllowed).
func (h *AccessHandler) viewerAllowed(r *http.Request, row model.SitePasscodeRow, userID string) (ok bool, email string, err error) {
	return accounts.ViewerAllowed(r.Context(), h.Database, row, userID)
}

// viewerGate answers a named-viewers site's file request for someone who may
// not open it, and reports whether it did. Signed out: the sign-in page (401).
// Signed in but not on the list: the private page (403). A cross-site
// subresource never gets in. The caller has set the private-cache headers.
func (h *AccessHandler) viewerGate(w http.ResponseWriter, r *http.Request, row model.SitePasscodeRow) bool {
	if crossSiteSubresource(r) || framedElsewhere(r) {
		writeOAuthHTMLError(h.pipeline(), w, http.StatusForbidden)
		return true
	}
	sess, ok := h.viewerSession(r, row.SiteID)
	if !ok {
		if h.startGateSSO(w, r) {
			return true
		}
		h.serveViewerPage(w, r, viewerPageSignIn, gateNext(r), "", "")
		return true
	}
	allowed, email, err := h.viewerAllowed(r, row, sess.UserID)
	if err != nil {
		log.Printf("viewers: site %s: %v", row.SiteID, err)
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return true
	}
	if !allowed {
		// The owner hears about it once a day per person, for a page visit
		// only (a stray subresource is not someone asking to be let in).
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
			h.emitSiteEvent(sites.SiteEvent{SiteID: row.SiteID, Name: "viewer.access_requested", Data: map[string]any{"visitor": sess.UserID}, Dedupe: 24 * time.Hour})
		}
		h.serveViewerPage(w, r, viewerPagePrivate, gateNext(r), email, "", h.ssoBaseURL)
		return true
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
		_ = h.StoreAccounts().TouchVisitorSession(r.Context(), sess.ID, h.Limits.VisitorSessionIdle)
	}
	return false
}

// viewersLetIn reports whether a data request may reach a named-viewers site
// as a visitor: the owner's or admin's key (passcodeAllows checks those
// first), a read from a preview page, or a same-origin request from the
// owner or a named viewer signed in on this host. Not a named-viewers site:
// true.
func (h *AccessHandler) viewersLetIn(r *http.Request, row model.SitePasscodeRow) (bool, error) {
	if !row.NamedViewers {
		return true, nil
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.previewRefererFor(r, row.SiteID) {
		return true, nil
	}
	// A same-origin request from the site's page, or a reading navigation
	// (a link to a stored file), never another site's subresource or frame.
	sess, ok := h.StrictVisitorSession(r, row.SiteID)
	if !ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !crossSiteSubresource(r) && !framedElsewhere(r) {
		sess, ok = h.viewerSession(r, row.SiteID)
	}
	if !ok {
		return false, nil
	}
	allowed, _, err := h.viewerAllowed(r, row, sess.UserID)
	return allowed, err
}

// framedElsewhere: another site's page is loading this one in a frame (every
// person's sites are one "site" to the browser until the base domain is on the
// Public Suffix List, so the Lax cookie would go along). A named-viewers
// site is never shown inside another site's page.
func framedElsewhere(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "iframe", "frame", "embed", "object", "fencedframe":
	default:
		return false
	}
	sfs := r.Header.Get("Sec-Fetch-Site")
	return sfs != "" && sfs != "same-origin" && sfs != "none"
}

// ViewersLetIn is viewersLetIn for a site id, for storage (site_storage.go):
// errors count as no.
func (h *AccessHandler) ViewersLetIn(r *http.Request, siteID string) bool {
	row, err := h.sitesSvc().GetSitePasscode(r.Context(), siteID)
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
	httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
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
func (h *AccessHandler) serveViewerPage(w http.ResponseWriter, r *http.Request, page viewerPage, next, email, msg string, apex ...string) {
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
	if !h.pipeline().ChromeProduct {
		w.Header().Set("Content-Security-Policy", hostPasscodeGateCSP)
	}
	if len(apex) > 0 && web.PublicBaseOrigin(apex[0]) != nil {
		w.Header().Set("Content-Security-Policy", strings.Replace(w.Header().Get("Content-Security-Policy"), "form-action 'self'", "form-action 'self' "+web.PublicBaseOrigin(apex[0]).String(), 1))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(h.viewerPageHTML(r, page, next, email, msg)))
}

func (h *AccessHandler) viewerPageHTML(r *http.Request, page viewerPage, next, email, msg string) string {
	host := httpx.HostName(r)
	var b strings.Builder
	if h.pipeline().ChromeProduct {
		b.WriteString(viewerGateHead)
	} else {
		b.Write(h.pipeline().HostedStatusPage(viewerGateHead))
	}
	b.WriteString(`<body><main class="gate"`)
	if page == viewerPageCode {
		sent := "0"
		if msg == "" {
			sent = "1"
		}
		b.WriteString(` data-sh-sent="` + sent + `"`)
	}
	b.WriteString(`>`)
	b.WriteString(`<p class="eyebrow">` + html.EscapeString(host) + `</p>`)
	hidden := `<input type="hidden" name="next" value="` + html.EscapeString(next) + `">`
	switch {
	case next == "":
		b.WriteString(`<h1>This site is private</h1>`)
		b.WriteString(`<p class="lead">Open it at its own address to sign in.</p>`)
	case page == viewerPagePrivate:
		b.WriteString(`<h1>You do not have access</h1>`)
		b.WriteString(`<div class="who"><span class="av">` + html.EscapeString(gateInitial(email)) + `</span><span>Signed in as <b>` + html.EscapeString(email) + `</b></span></div>`)
		b.WriteString(`<p class="lead">This address is not on the owner's list. Ask them to add it, or sign in with another email.</p>`)
		b.WriteString(gateErrHTML("sh-deny-err", msg))
		b.WriteString(`<form method="post" action="/v1/site-signout">` + hidden)
		b.WriteString(`<button type="submit" name="then" value="switch" class="primary">Use another email</button>`)
		b.WriteString(`<button type="submit" name="then" value="out" class="secondary">Sign out</button></form>`)
	case page == viewerPageCode:
		b.WriteString(`<h1>Check your email</h1>`)
		b.WriteString(`<div class="lead">We sent a 6-digit code to <b>` + html.EscapeString(email) + `</b>. `)
		b.WriteString(`<form class="inline" method="post" action="/v1/site-signout">` + hidden)
		b.WriteString(`<button type="submit" name="then" value="switch" class="link">Change</button></form></div>`)
		b.WriteString(`<form method="post" action="/v1/site-signin">` + hidden)
		b.WriteString(`<input type="hidden" name="email" value="` + html.EscapeString(email) + `">`)
		b.WriteString(`<label for="sh-code">Code</label>`)
		cls := "code"
		described := ""
		if msg != "" {
			cls = "code bad"
			described = ` aria-describedby="sh-code-err"`
		}
		b.WriteString(`<input id="sh-code" class="` + cls + `" name="code" type="text" inputmode="numeric" autocomplete="one-time-code" enterkeyhint="done" aria-label="Code" autofocus required` + described + `>`)
		b.WriteString(gateErrHTML("sh-code-err", msg))
		b.WriteString(`<button type="submit" class="primary">Sign in</button></form>`)
		b.WriteString(`<div class="row"><form class="inline" method="post" action="/v1/site-signin">` + hidden)
		b.WriteString(`<input type="hidden" name="email" value="` + html.EscapeString(email) + `">`)
		b.WriteString(`<button type="submit" class="link" data-sh-resend>Send a new code</button></form>`)
		if visitorGmail(email) {
			b.WriteString(`<a class="link" href="` + html.EscapeString(visitorGmailURL(r.UserAgent())) + `" target="_blank" rel="noopener noreferrer">Open Gmail</a>`)
		}
		b.WriteString(`</div>`)
		b.WriteString(gateAssistTag)
	default:
		b.WriteString(`<h1>This site is private</h1>`)
		b.WriteString(`<p class="lead">Only people the owner named can open it. Sign in with the email they added.</p>`)
		if h.ViewerGoogle && strings.HasPrefix(next, "/") {
			scheme := "http://"
			if httpx.IsHTTPS(r) {
				scheme = "https://"
			}
			start := "/v1/visitor/oauth/google?return_to=" + url.QueryEscape(scheme+host+next)
			b.WriteString(`<a class="gbtn" href="` + html.EscapeString(start) + `">` + gateGoogleSVG + `<span>Continue with Google</span></a>`)
			b.WriteString(`<div class="or">or</div>`)
		}
		b.WriteString(`<form method="post" action="/v1/site-signin">` + hidden)
		b.WriteString(`<label for="sh-email">Email</label>`)
		bad := ""
		if msg != "" {
			bad = ` class="bad" aria-describedby="sh-email-err"`
		}
		b.WriteString(`<input id="sh-email" name="email" type="email" aria-label="Email" placeholder="you@example.com" autocomplete="email" autofocus required maxlength="254"` + bad + `>`)
		b.WriteString(gateErrHTML("sh-email-err", msg))
		b.WriteString(`<button type="submit" class="primary">Continue</button></form>`)
		b.WriteString(`<p class="fine">We email you a 6-digit code. No password, no account to make.</p>`)
	}
	b.WriteString(`</main></body></html>
`)
	return b.String()
}

func gateErrHTML(id, msg string) string {
	if msg == "" {
		return ""
	}
	return `<p class="err" id="` + id + `" role="alert">` + gateInfoSVG + `<span>` + html.EscapeString(msg) + `</span></p>`
}

func gateInitial(email string) string {
	email = strings.TrimSpace(email)
	r, size := utf8.DecodeRuneInString(email)
	if size == 0 || r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

func visitorGmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	switch email[at+1:] {
	case "gmail.com", "googlemail.com":
		return true
	default:
		return false
	}
}

func visitorGmailURL(ua string) string {
	if strings.Contains(strings.ToLower(ua), "android") {
		return "intent://#Intent;scheme=googlegmail;package=com.google.android.gm;S.browser_fallback_url=https%3A%2F%2Fmail.google.com%2Fmail%2F;end"
	}
	return "https://mail.google.com/mail/"
}

// --- Signing in and out from the pages ---

// viewerSiteForForm finds the named-viewers site a form on this host is
// for (as the passcode form does). ok=false: nothing to sign in to here.
func (h *AccessHandler) viewerSiteForForm(r *http.Request, next string) (model.SitePasscodeRow, bool, error) {
	userID, name, ok, err := h.passcodeSiteForHost(r.Context(), httpx.HostName(r), next)
	if err != nil || !ok {
		return model.SitePasscodeRow{}, false, err
	}
	row, err := h.sitesSvc().GetSitePasscodeByName(r.Context(), userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	if !row.NamedViewers || row.Offline || h.Disk.IsSuspended(row.UserID, row.Name) {
		return row, false, nil
	}
	return row, true, nil
}

// viewerSignIn is POST /v1/site-signin, the sign-in page's form: email alone
// sends a code (the code page follows); email and code sign the visitor in to
// this site on this host and 303 back to next. Only from a page on this very
// host (login CSRF), only for the site this host names. Everyone gets a code,
// listed or not, so the form never says who is on the list.
func (h *AccessHandler) viewerSignIn(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bad request")
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "sign in on the site's own page", Code: "origin_not_allowed"})
		return
	}
	row, ok, err := h.viewerSiteForForm(r, next)
	if err != nil {
		log.Printf("viewers sign-in %s: %v", httpx.HostName(r), err)
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	if !ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if !h.VisitorAuthLimiter.Allow(httpx.ClientIP(r)) {
		h.serveViewerPage(w, r, viewerPageSignIn, next, "", web.SignInMessages["rate_limited"])
		return
	}
	address, code := r.PostForm.Get("email"), r.PostForm.Get("code")
	if strings.TrimSpace(code) == "" {
		h.viewerSendCode(w, r, row.SiteID, next, address)
		return
	}
	h.viewerVerifyCode(w, r, row.SiteID, next, address, code)
}

// viewerSendCode is the sign-in form with an email alone: the code page
// follows, or the sign-in page with why no code was sent.
func (h *AccessHandler) viewerSendCode(w http.ResponseWriter, r *http.Request, siteID, next, address string) {
	if h.noEmail {
		h.serveViewerPage(w, r, viewerPageSignIn, next, "", web.SignInMessages["email_unavailable"])
		return
	}
	sent, _, status, body := issueEmailCode(r.Context(), h.Database, h.Mailer, h.EmailLimiter, address, "", "visitor", sql.NullString{String: siteID, Valid: true}, sql.NullString{}, httpx.ClientIP(r), h.geoBlock, h.Limits.SigninCodeTTL)
	if status != 0 {
		h.serveViewerPage(w, r, viewerPageSignIn, next, "", signInMessageOr(body.Error))
		return
	}
	h.serveViewerPage(w, r, viewerPageCode, next, sent, "")
}

// viewerVerifyCode is the code form: the visitor is signed in to this site
// on this host and sent back to next, or the code page says why not.
func (h *AccessHandler) viewerVerifyCode(w http.ResponseWriter, r *http.Request, siteID, next, address, code string) {
	user, created, status, body := VerifyEmailCode(r.Context(), h.Database, h.EmailLimiter, VerifyRequest{Email: address, Code: code}, "visitor", sql.NullString{String: siteID, Valid: true}, h.Ed.Identity().RandomHandles, h.Limits.SigninCodeTTL)
	if status != 0 {
		h.serveViewerPage(w, r, viewerPageCode, next, strings.ToLower(strings.TrimSpace(address)), signInMessageOr(body.Error))
		return
	}
	if created {
		accounts.RecordSignup(r.Context(), h.Database, user.ID, model.Signup{Source: model.SignupVisitor, Method: model.SignupMethodEmail})
	}
	start, err := h.StoreAccounts().StartVisitorSession(r.Context(), user.ID, siteID, httpx.HostName(r), h.Limits.VisitorSessionTTL, h.Limits.VisitorSessionIdle)
	if errors.Is(err, model.ErrAccountSuspended) {
		// No session for a suspended account: back to the page, still
		// signed out.
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if err != nil {
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	h.VisitorSignedIn(r, siteID, user.ID, start.First)
	setVisitorSessionCookie(w, r, hex.EncodeToString(start.ID), int(h.Limits.VisitorSessionTTL/time.Second))
	log.Printf("viewers_sign_in site_id=%s user_id=%s", siteID, user.ID)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// signInMessageOr is msg, or the generic sign-in failure when it is empty.
func signInMessageOr(msg string) string {
	if msg == "" {
		return web.SignInMessages["unknown"]
	}
	return msg
}

// viewerSignOut is POST /v1/site-signout, the private page's buttons: ends
// this host's visitor session and 303s back to next, which then shows the
// sign-in page. Only from a page on this very host.
func (h *AccessHandler) viewerSignOut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "bad request")
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	if !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "sign out on the site's own page", Code: "origin_not_allowed"})
		return
	}
	back, err := h.endSiteSSO(w, r, next)
	if err != nil {
		web.RenderServiceErrorPage(w, r, h.pipeline())
		return
	}
	if back != "" {
		next = back
	}

	setVisitorSessionCookie(w, r, "", 0)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// --- Owner API ---

// siteAccessResponse is what the owner routes answer.
type siteAccessResponse struct {
	Site              string             `json:"site"`
	Access            string             `json:"access"`
	Viewers           []model.SiteViewer `json:"viewers"`
	ViewerLimit       int                `json:"viewer_limit"`
	PasscodeProtected bool               `json:"passcode_protected"`
	Added             []string           `json:"added,omitempty"`
	AlreadyListed     []string           `json:"already_listed,omitempty"`
	Removed           string             `json:"removed,omitempty"`
	Note              string             `json:"note,omitempty"`
}

func (h *AccessHandler) siteAccessState(r *http.Request, site model.Site) (siteAccessResponse, error) {
	row, err := h.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		return siteAccessResponse{}, err
	}
	viewers, err := h.sitesSvc().ListSiteViewers(r.Context(), site.ID)
	if err != nil {
		return siteAccessResponse{}, err
	}
	access := web.AccessAnyone
	if row.NamedViewers {
		access = web.AccessSpecific
	}
	return siteAccessResponse{Site: site.Name, Access: access, Viewers: viewers, ViewerLimit: h.Limits.SiteViewersMax, PasscodeProtected: row.Enc != nil}, nil
}

func (h *AccessHandler) writeSiteAccess(w http.ResponseWriter, r *http.Request, site model.Site, resp siteAccessResponse) {
	state, err := h.siteAccessState(r, site)
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	state.Added, state.AlreadyListed, state.Removed, state.Note = resp.Added, resp.AlreadyListed, resp.Removed, resp.Note
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, state)
}

// getSiteAccess is GET /v1/sites/{sitename}/access: who can open the site
// (access "anyone" or "specific") and its named viewers.
func (h *AccessHandler) getSiteAccess(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	site, err := h.sitesSvc().GetSiteByUser(r.Context(), user.ID, strings.TrimSpace(r.PathValue("sitename")))
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	h.writeSiteAccess(w, r, site, siteAccessResponse{})
}

// turnOnNamedViewers sets access to specific: the marker first, then the
// database, so a failure in between leaves the site closed, never open.
func (h *AccessHandler) turnOnNamedViewers(r *http.Request, site model.Site, row model.SitePasscodeRow) error {
	if row.NamedViewers {
		return nil
	}
	if err := accounts.NamedViewersRefusal(row, h.gateSite(site)); err != nil {
		return err
	}
	if err := h.sitesSvc().SetSiteAccess(r.Context(), site, row, web.AccessSpecific); err != nil {
		return err
	}
	log.Printf("site_access access=specific site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	return nil
}

// putSiteAccess is PUT /v1/sites/{sitename}/access {"access": "anyone" |
// "specific"}. specific: only the owner and the named viewers can open the
// site (with none named yet, only the owner). anyone: open to everyone with
// the address again; the list is kept for next time.
func (h *AccessHandler) putSiteAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Access string `json:"access"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Access != web.AccessAnyone && req.Access != web.AccessSpecific) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `invalid JSON body (expected {"access":"anyone"} or {"access":"specific"})`, Code: "invalid_request"})
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	row, err := h.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	note := ""
	if req.Access == web.AccessSpecific {
		if err := h.turnOnNamedViewers(r, site, row); err != nil {
			web.WriteAccessError(w, err)
			return
		}
		note = "Only you and the named viewers can open the site now; everyone else sees a sign-in page. Viewers sign in on the site with the email you named."
	} else {
		if row.NamedViewers {
			if err := h.sitesSvc().SetSiteAccess(r.Context(), site, row, web.AccessAnyone); err != nil {
				web.WriteAccessMutationError(w, err)
				return
			}

			log.Printf("site_access access=anyone site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
		}
		note = "Anyone with the address can open the site. The named viewers are kept, in case you turn it back on."
	}
	h.writeSiteAccess(w, r, site, siteAccessResponse{Note: note})
}

// addSiteViewers is POST /v1/sites/{sitename}/viewers {"emails": [...]}: names
// viewers and sets access to specific (one call: "only mom and dad").
func (h *AccessHandler) addSiteViewers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Emails []string `json:"emails"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || len(req.Emails) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `invalid JSON body (expected {"emails":["person@example.com"]})`, Code: "invalid_request"})
		return
	}
	emails, err := accounts.ViewerEmails(req.Emails, h.Limits.SiteViewersMax)
	if err != nil {
		web.WriteAccessError(w, err)
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	resp, err := h.nameViewers(r, site, emails)
	if err != nil {
		web.WriteAccessError(w, err)
		return
	}
	h.writeSiteAccess(w, r, site, resp)
}

// nameViewers adds the emails not yet named and turns named viewers on: the
// list first, then the gate, so a failure in between leaves the site as it
// was, with the names ready. The caller holds the site's lock.
func (h *AccessHandler) nameViewers(r *http.Request, site model.Site, emails []string) (siteAccessResponse, error) {
	row, err := h.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		return siteAccessResponse{}, err
	}
	if !row.NamedViewers {
		if err := accounts.NamedViewersRefusal(row, h.gateSite(site)); err != nil {
			return siteAccessResponse{}, err
		}
	}
	fresh, already, err := h.StoreAccounts().NewViewers(r.Context(), site.ID, emails, h.Limits.SiteViewersMax)
	if err != nil {
		return siteAccessResponse{}, err
	}
	for _, e := range fresh {
		if _, err := h.sitesSvc().AddSiteViewer(r.Context(), site.ID, e); err != nil {
			return siteAccessResponse{}, err
		}
	}
	if err := h.turnOnNamedViewers(r, site, row); err != nil {
		return siteAccessResponse{}, err
	}
	log.Printf("site_viewers added=%d site_id=%s name=%s by=%s", len(fresh), site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "Only you and the named viewers can open the site. Tell them to open its address and sign in with the email you named (an emailed code, or Google for a Google address)."
	return siteAccessResponse{Added: fresh, AlreadyListed: already, Note: note}, nil
}

// removeSiteViewer is DELETE /v1/sites/{sitename}/viewers/{email}: takes one
// person off the list; their next request is refused. Access stays as it is:
// removing the last viewer leaves the site open only to its owner.
func (h *AccessHandler) removeSiteViewer(w http.ResponseWriter, r *http.Request) {
	e, ok := authrules.NormalEmail(r.PathValue("email"))
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "not an email address", Code: "invalid_email"})
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	removed, err := h.sitesSvc().RemoveSiteViewer(r.Context(), site.ID, e)
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	if !removed {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: e + " is not a named viewer of this site", Code: "viewer_not_found"})
		return
	}
	// A removed named viewer stops receiving push too: RemoveSiteViewer
	// already ran appsites.Deps.DeleteViewerPush for every caller.
	log.Printf("site_viewers removed=1 site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := e + " can no longer open the site."
	h.writeSiteAccess(w, r, site, siteAccessResponse{Removed: e, Note: note})
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES for visitor email-code sign-ins.
func (h *AccessHandler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) {
	h.geoBlock = guard
}

// SetVisitorSignIn records how visitors can sign in on this install: email
// codes (email) and the OAuth providers configured (providers).
func (h *AccessHandler) SetVisitorSignIn(email bool, providers []string) {
	h.noEmail = !email
	h.noVisitorSignIn = accounts.NoVisitorSignIn(email, providers)
	h.ViewerGoogle = false
	for _, p := range providers {
		h.ViewerGoogle = h.ViewerGoogle || p == "google"
	}
}
