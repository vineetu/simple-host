package accesshttp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

const oauthStateTTL = 10 * time.Minute

var OAuthHTMLFailed = web.Themed(`<!DOCTYPE html><html><head><meta charset="utf-8"><!--sh:theme--><title>Sign-in failed</title></head><body><p>Sign-in failed. Close this tab and try again from the site.</p></body></html>`)

// OAuthHTMLSignupBlocked names no country and no reason beyond "your
// region" — the same wording the email-code path returns as JSON.
var OAuthHTMLSignupBlocked = web.Themed(`<!DOCTYPE html><html><head><meta charset="utf-8"><!--sh:theme--><title>Sign-in unavailable</title></head><body><p>` + web.SignupBlockedMessage + `</p></body></html>`)

func (h *OAuthHandler) writeOAuthHTMLBlocked(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(h.pipeline().HostedStatusPage(OAuthHTMLSignupBlocked))
}

type OAuthSettings struct {
	PublicBaseURL, SiteDomain, ContentHost, CNAMETarget, CustomDomainIP, OIDCLabel string
	CustomDomainIPAlso                                                             []string
	EmailEnabled                                                                   bool
	PasswordEnabled, PasswordSelfSignup                                            bool
	SignIn                                                                         *accounts.ProviderSignIn
}

type OAuthHandler struct {
	visitorWorkerOnce sync.Once
	visitorWorker     *accounts.VisitorAuthWorker
	httpCaches        httpx.Caches
	ed                *edition.Edition
	Limits            edition.Limits
	// Access is the visitor gate of the sites sign-in returns to: their
	// addresses (Hosts) and whether one takes a sign-in now. Nil (tests): no
	// person hosts, SITE_DOMAIN alone with exact matching, and no site signs
	// anyone in.
	Access *AccessHandler

	Database      *sql.DB
	Accounts      *accounts.Service
	Cfg           OAuthSettings
	SignIn        *accounts.ProviderSignIn
	IPLimiter     *httpx.RateLimiter
	SSOKeyLimiter *httpx.RateLimiter
	ssoEnabled    bool
	LookupIP      func(ctx context.Context, host string) ([]net.IP, error)
	LookupCNAME   func(ctx context.Context, host string) (string, error)

	// geoBlock gates new-account creation (a first OAuth sign-in, owner or
	// visitor) by the request's country (SIGNUP_BLOCKED_COUNTRIES). Zero
	// value: off. See signupgeo.go.
	geoBlock accounts.SignupGeoBlock

	// assets is the asset/chrome pipeline for ed and cfg.PublicBaseURL
	// (sso.go's status and sign-in pages); read it through pipeline().
	assets     *web.AssetPipeline
	assetsOnce sync.Once
}

// pipeline is h's asset pipeline. NewOAuthHandler builds its edition; a bare
// &OAuthHandler{} (tests) gets one for its (possibly nil, so Host) edition on
// first use.
func (h *OAuthHandler) pipeline() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = web.NewAssetPipeline(h.ed, h.Cfg.PublicBaseURL)
		}
	})
	return h.assets
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES for OAuth sign-ins.
func (h *OAuthHandler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) {
	h.geoBlock = guard
}

// NewOAuthHandler builds sign-in for the sites access guards.
func NewOAuthHandler(database *sql.DB, service *accounts.Service, cfg OAuthSettings, access *AccessHandler, editions ...*edition.Edition) *OAuthHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}

	lim := ed.Limits()
	ipLimiter := httpx.NewRateLimiterFor(lim.RateVisitorOAuth.Burst, lim.RateVisitorOAuth.Every)
	ssoKeyLimiter := httpx.NewRateLimiterFor(lim.RateVisitorOAuth.Burst, lim.RateVisitorOAuth.Every)
	h := &OAuthHandler{
		ed:            ed,
		Limits:        lim,
		Access:        access,
		Database:      database,
		Accounts:      service,
		Cfg:           cfg,
		SignIn:        cfg.SignIn,
		IPLimiter:     ipLimiter,
		SSOKeyLimiter: ssoKeyLimiter,
		LookupIP:      defaultLookupIP,
		LookupCNAME:   net.DefaultResolver.LookupCNAME,
	}
	h.httpCaches.Rate("http.oauth.ipLimiter", ipLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.oauth.ssoKeyLimiter", ssoKeyLimiter, 10*time.Minute, 30*time.Minute)
	return h
}

// hosts is Access's addressing, nil without it.
func (h *OAuthHandler) hosts() *appsites.Hosts {
	if h.Access == nil {
		return nil
	}
	return h.Access.Hosts
}

// bases are the domains people's addresses live under (SITE_DOMAIN, plus
// SITE_BASE_DOMAIN while it moves; sitebase.go), or SITE_DOMAIN alone without
// access.
func (h *OAuthHandler) bases() []string {
	if hosts := h.hosts(); hosts != nil {
		if b := hosts.ServedBases(); len(b) > 0 {
			return b
		}
	}
	return []string{h.Cfg.SiteDomain}
}

// sameAddress: a and b are one address across the served bases, or an exact
// match without access.
func (h *OAuthHandler) sameAddress(a, b string) bool {
	if hosts := h.hosts(); hosts != nil {
		return hosts.SameUserHost(a, b)
	}
	return strings.EqualFold(a, b)
}

func (h *OAuthHandler) Register(mux *http.ServeMux) {
	h.registerSSO(mux)
	// Always register the string literals, even when no provider is enabled.
	mux.HandleFunc("GET /v1/auth/oauth/providers", h.listProviders)
	// Account Google sign-in answers only on the app's own address
	// (accountauth.go); a hand-written start link on a site's host is sent on
	// to the site-host visitor start; providers stays public: auth.js reads it
	// from a site.
	mux.Handle("GET /v1/auth/oauth/{provider}/callback", web.AccountAuthOnly(h.Cfg.PublicBaseURL, httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.callback))))
	mux.Handle("GET /v1/auth/oauth/{provider}", siteStartOrAccountAuth(h.Cfg.PublicBaseURL, httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.start))))
	// Visitor sign-in starts on the site's own host, which binds it to this
	// browser with a nonce cookie (login-CSRF fix; see StartOnSite).
	mux.Handle("GET /v1/visitor/oauth/{provider}", httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.StartOnSite)))
}

// knownOAuthProviderName is the fixed set of provider names this handler
// ever serves. h.providers already reflects which of them are configured;
// this just stops a lookup under any other path segment.
func knownOAuthProviderName(name string) bool {
	return name == "google" || name == "github" || name == "oidc"
}

func (h *OAuthHandler) listProviders(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, 3)
	if h.SignIn.Has("google") {
		names = append(names, "google")
	}
	if h.SignIn.Has("github") {
		names = append(names, "github")
	}
	resp := map[string]any{"providers": names, "email_enabled": h.Cfg.EmailEnabled, "sso_enabled": publicBaseHost(h.Cfg.PublicBaseURL) != ""}
	if h.Cfg.PasswordEnabled {
		resp["password"] = map[string]bool{"enabled": true, "self_signup": h.Cfg.PasswordSelfSignup}
	}
	if h.SignIn.Has("oidc") {
		names = append(names, "oidc")
		resp["providers"] = names
		resp["oidc_label"] = h.Cfg.OIDCLabel
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *OAuthHandler) start(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if !h.SignIn.Has(name) || !knownOAuthProviderName(name) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return
	}

	sanitized, siteID, host, purpose, err := h.SanitizeReturnTo(r.Context(), r.URL.Query().Get("return_to"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid return_to"})
		return
	}

	// A visitor sign-in has to start on the site's own host, where it can be
	// bound to the browser (StartOnSite). Send older pages and hand-written
	// links there; the apex never issues an unbound site sign-in.
	if purpose == "site" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, siteStartURL(sanitized, name), http.StatusFound)
		return
	}

	h.beginOAuth(w, r, name, sanitized, host, siteID, purpose, "")
}

// siteSignInNonceTTL is how long a started visitor sign-in stays redeemable
// in the browser that started it (the OAuth state lives as long).
const siteSignInNonceTTL = oauthStateTTL

const (
	VisitorNonceCookieHost = "__Host-sh_vnonce"
	VisitorNonceCookieHTTP = "sh_vnonce"
)

// siteStartURL is the site-host start for a validated site return_to.
func siteStartURL(returnTo, provider string) string {
	u, _ := url.Parse(returnTo)
	return u.Scheme + "://" + u.Host + "/v1/visitor/oauth/" + url.PathEscape(provider) + "?return_to=" + url.QueryEscape(returnTo)
}

// StartOnSite handles GET /v1/visitor/oauth/{provider} on a site's own host
// (<site>.<handle> host, person host, claimed name or custom domain). It sets
// a short-lived host-only nonce cookie here and stores only its SHA-256 with
// the OAuth state; the callback copies that hash onto the establish code, and
// establish (on this same host) sets the session cookie only in a browser
// holding the nonce. So a sign-in someone completed in their own browser
// cannot be finished in a victim's (login CSRF).
func (h *OAuthHandler) StartOnSite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if h.ssoEnabled {
		h.startSSO(w, r)
		return
	}
	if !signInNavigationAllowed(r) {
		writeOAuthHTMLError(h.pipeline(), w, 403)
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if !h.SignIn.Has(name) || !knownOAuthProviderName(name) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return
	}
	sanitized, siteID, host, purpose, err := h.SanitizeReturnTo(r.Context(), r.URL.Query().Get("return_to"))
	if err != nil || purpose != "site" || !strings.EqualFold(host, httpx.HostName(r)) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid return_to"})
		return
	}
	if !h.siteSignInAllowed(w, r, siteID.String) {
		return
	}
	nonce, err := RandomHex(32)
	if err != nil {
		log.Printf("oauth: random nonce: %v", err)
		httpx.WriteInternal(w)
		return
	}
	h.beginOAuth(w, r, name, sanitized, host, siteID, purpose, nonce)
}

// setVisitorNonceCookie sets (maxAge > 0) or clears (maxAge < 0) the sign-in
// nonce cookie on this host. Over HTTPS it is __Host-: host-only, so no
// sibling under the same parent domain can plant or overwrite it.
func setVisitorNonceCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := httpx.IsHTTPS(r)
	name := VisitorNonceCookieHTTP
	if secure {
		name = VisitorNonceCookieHost
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// visitorNonceCookie reads the nonce cookie strictly: over HTTPS only the
// __Host- one counts (a sibling host can plant a plain one, never this).
func visitorNonceCookie(r *http.Request) string {
	name := VisitorNonceCookieHTTP
	if httpx.IsHTTPS(r) {
		name = VisitorNonceCookieHost
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// beginOAuth stores the state and redirects to the provider. A site sign-in
// passes the nonce: only its hash is stored, and the nonce itself goes into a
// cookie on this host and nowhere else.
func (h *OAuthHandler) beginOAuth(w http.ResponseWriter, r *http.Request, name, sanitized, host string, siteID sql.NullString, purpose, nonce string) {
	authURL, failure := h.SignIn.Begin(r.Context(), name, sanitized, host, siteID, purpose, nonce)
	if failure != nil {
		httpx.WriteJSON(w, failure.Status, httpx.ErrorResponse{Error: failure.Message, Code: failure.Code})
		return
	}
	if nonce != "" {
		setVisitorNonceCookie(w, r, nonce, int(siteSignInNonceTTL/time.Second))
		w.Header().Set("Cache-Control", "no-store")
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if !h.SignIn.Has(name) || !knownOAuthProviderName(name) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return
	}

	q := r.URL.Query()
	out, failure := h.SignIn.Callback(r.Context(), accounts.ProviderCallbackInput{
		Name: name, State: strings.TrimSpace(q.Get("state")), Code: q.Get("code"), ProviderError: q.Get("error"),
		ClientIP: httpx.ClientIP(r), GeoBlock: h.geoBlock,
		RandomHandles: h.ed.Identity().RandomHandles, AuthTTL: h.Limits.SigninCodeTTL,
		Attribution: func(ctx context.Context, q sqlkit.Querier, returnTo, provider string) model.Signup {
			return SignupForOwnerReturnTo(ctx, q, r, returnTo, provider)
		},
	})
	if failure != nil {
		if failure.Blocked {
			h.writeOAuthHTMLBlocked(w)
		} else if failure.Refusal != "" {
			writeOAuthHTMLRefused(h.pipeline(), w, failure.Refusal)
		} else {
			writeOAuthHTMLError(h.pipeline(), w, failure.Status)
		}
		return
	}
	dest := ""
	if out.State.Purpose == "owner" {
		dest = ownerLandingURL(out.State.ReturnTo, h.Cfg.PublicBaseURL, out.LinkToken)
	} else {
		scheme := "https"
		if u, err := url.Parse(out.State.ReturnTo); err == nil && u.Scheme != "" {
			scheme = u.Scheme
		}
		dest = scheme + "://" + out.State.Host + "/v1/visitor/establish?once=" + url.QueryEscape(out.Once)
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, dest, http.StatusFound)
}

func writeOAuthHTMLError(assets *web.AssetPipeline, w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(assets.HostedStatusPage(OAuthHTMLFailed))
}

// oauthHTMLRefusedTemplate carries a placeholder for the per-refusal
// message (oauth.RefusalError.Message: a short, internals-free sentence),
// filled in and HTML-escaped at request time.
var oauthHTMLRefusedTemplate = web.Themed(`<!DOCTYPE html><html><head><meta charset="utf-8"><!--sh:theme--><title>Sign-in refused</title></head><body><p>Sign-in refused: __MESSAGE__</p></body></html>`)

// writeOAuthHTMLRefused shows a company sign-in's business-rule refusal
// (unverified email, domain not allowed): 403, themed like OAuthHTMLFailed.
func writeOAuthHTMLRefused(assets *web.AssetPipeline, w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	page := strings.Replace(oauthHTMLRefusedTemplate, "__MESSAGE__", html.EscapeString(message), 1)
	_, _ = w.Write(assets.HostedStatusPage(page))
}

// resolveUser maps a provider identity onto one users row.
// Re-logins key only on (provider, provider_user_id). A missing or unverified
// email is refused and creates nothing. The signup geo-block is checked by
// the caller (callback), unconditionally, before this runs at all — it does
// not distinguish a new account from an existing one for OAuth.

// SanitizeReturnTo validates return_to at start time. On success it returns
// parsed.String(), the resolved site_id (null for owner), hostname, and purpose.
func (h *OAuthHandler) SanitizeReturnTo(ctx context.Context, raw string) (string, sql.NullString, string, string, error) {
	parsed, err := parseAbsoluteReturnTo(raw, h.Cfg.PublicBaseURL)
	if err != nil {
		return "", sql.NullString{}, "", "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}

	if dashboardReturnToOK(parsed, h.Cfg.PublicBaseURL) || ssoReturnToOK(parsed, h.Cfg.PublicBaseURL) {
		return parsed.String(), sql.NullString{}, "", "owner", nil
	}
	if connectReturnToOK(parsed, h.Cfg.PublicBaseURL) {
		return parsed.String(), sql.NullString{}, "", "owner", nil
	}

	// A person host (<handle>.<SITE_DOMAIN>) is that person's own origin: the
	// site is the first path segment and must be theirs. The platform owns the
	// zone, so the account's handle is the proof (no DNS lookup).
	if hosts := h.hosts(); hosts != nil {
		if siteID, ok := hosts.ReturnSite(ctx, host, parsed.Path); ok {
			return parsed.String(), sql.NullString{String: siteID, Valid: true}, host, "site", nil
		}
	}

	// A claimed <name>.<SITE_DOMAIN> address is a site's own domain: sign-in
	// returns there like to a custom domain. The platform owns that zone, so
	// the binding itself is the proof (no DNS lookup).
	if h.isClaimedPlatformSubdomain(host) {
		info, err := appsites.GetSiteByCustomDomain(ctx, h.Database, host)
		if err != nil || !info.VerifiedAt.Valid || !h.sameAddress(info.Domain, host) {
			return "", sql.NullString{}, "", "", errInvalidReturnTo
		}
		return parsed.String(), sql.NullString{String: info.SiteID, Valid: true}, host, "site", nil
	}

	for _, b := range h.bases() {
		if isRejectedPlatformHost(host, b, h.Cfg.ContentHost, publicBaseHost(h.Cfg.PublicBaseURL)) {
			return "", sql.NullString{}, "", "", errInvalidReturnTo
		}
	}

	info, err := appsites.GetSiteByCustomDomain(ctx, h.Database, host)
	if err != nil {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	if !info.VerifiedAt.Valid || !strings.EqualFold(info.Domain, host) {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	if err := h.proveCustomDomainControl(ctx, host); err != nil {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	return parsed.String(), sql.NullString{String: info.SiteID, Valid: true}, host, "site", nil
}

var errInvalidReturnTo = errors.New("invalid return_to")

// isClaimedPlatformSubdomain: host is a single-label <name>.<SITE_DOMAIN> that
// is not one of the platform's own hosts. Whether a site holds it is the
// caller's lookup.
func (h *OAuthHandler) isClaimedPlatformSubdomain(host string) bool {
	return appsites.ClaimedReturnHost(host, h.bases(), h.Cfg.ContentHost, h.Cfg.CNAMETarget, publicBaseHost(h.Cfg.PublicBaseURL))
}

func parseAbsoluteReturnTo(raw, publicBaseURL string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errInvalidReturnTo
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Opaque != "" {
		return nil, errInvalidReturnTo
	}
	if parsed.User != nil {
		return nil, errInvalidReturnTo
	}
	httpOK := publicBaseIsHTTP(publicBaseURL)
	switch parsed.Scheme {
	case "https":
	case "http":
		if !httpOK {
			return nil, errInvalidReturnTo
		}
	default:
		return nil, errInvalidReturnTo
	}
	if port := parsed.Port(); port != "" {
		schemeDefault := (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80")
		if !schemeDefault && port != publicBasePort(publicBaseURL) {
			return nil, errInvalidReturnTo
		}
	}
	return parsed, nil
}

func publicBaseIsHTTP(publicBaseURL string) bool {
	u, err := url.Parse(publicBaseURL)
	return err == nil && strings.EqualFold(u.Scheme, "http")
}

func publicBaseHost(publicBaseURL string) string {
	u, err := url.Parse(publicBaseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func publicBasePort(publicBaseURL string) string {
	u, err := url.Parse(publicBaseURL)
	if err != nil {
		return ""
	}
	return u.Port()
}

// ownerReturnToOK is the dashboard allow-list: exact PUBLIC_BASE_URL host,
// path Clean is "/", empty query, empty fragment. Apex is still rejected as
// a site return_to via isRejectedPlatformHost.
func ownerReturnToOK(parsed *url.URL, publicBaseURL string) bool {
	if parsed == nil {
		return false
	}
	base, err := url.Parse(publicBaseURL)
	if err != nil || base.Host == "" {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, base.Scheme) {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), base.Hostname()) {
		return false
	}
	pPort, bPort := parsed.Port(), base.Port()
	if pPort != bPort {
		// Treat omitted vs scheme-default as the same port.
		if !((pPort == "" || isSchemeDefaultPort(parsed.Scheme, pPort)) &&
			(bPort == "" || isSchemeDefaultPort(base.Scheme, bPort))) {
			return false
		}
		if pPort != "" && bPort != "" && pPort != bPort {
			return false
		}
	}
	if parsed.User != nil {
		return false
	}
	if path.Clean("/"+parsed.Path) != "/" {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

// Owner Google sign-in returns to one of two pages, and in both the return
// address carries cn: the SHA-256 (base64url) of a random nonce the page kept
// in its own tab's sessionStorage before starting. The one-time token this
// flow issues is stored with that hash and redeemable only with the nonce
// itself, so a link someone else crafted — carrying a token for their own
// account — cannot sign a victim's browser in as them (login CSRF).

// dashboardReturnToOK: this origin, path "/", exactly one cn, and only
// optional same-visit attribution fields. These never grant authority.
func dashboardReturnToOK(parsed *url.URL, publicBaseURL string) bool {
	if parsed == nil || parsed.Fragment != "" {
		return false
	}
	q := parsed.Query()
	// Bounded the same as arrivalFrom/arrivalText (signup.go) so an oversized
	// value can't bloat the stored OAuth state row or the landing redirect.
	for key, max := range map[string]int{"from": 40, "referrer": 100, "utm_source": 80, "utm_medium": 80, "utm_campaign": 80, "utm_term": 80, "utm_content": 80} {
		if len(q[key]) > 1 || len(q.Get(key)) > max {
			return false
		}
		q.Del(key)
	}
	if len(q) != 1 || len(q["cn"]) != 1 || !accounts.NonceHashShape.MatchString(q.Get("cn")) {
		return false
	}
	same := *parsed
	same.RawQuery = ""
	return ownerReturnToOK(&same, publicBaseURL)
}

// connectReturnToOK: this origin, path exactly /oauth/authorize, no fragment,
// no sign-in token already in the query, and exactly one cn.
func connectReturnToOK(parsed *url.URL, publicBaseURL string) bool {
	if parsed == nil || parsed.User != nil || parsed.Fragment != "" || parsed.Path != "/oauth/authorize" {
		return false
	}
	q := parsed.Query()
	if _, has := q["token"]; has {
		return false
	}
	if cn := q["cn"]; len(cn) != 1 || !accounts.NonceHashShape.MatchString(cn[0]) {
		return false
	}
	same := *parsed
	same.Path, same.RawQuery, same.RawPath = "/", "", ""
	return ownerReturnToOK(&same, publicBaseURL)
}

// ownerLandingURL is where an owner Google sign-in lands with its one-time
// token: the page it started from, with the token appended next to cn.
func ownerLandingURL(returnTo, publicBaseURL, linkToken string) string {
	base, _ := url.Parse(strings.TrimRight(publicBaseURL, "/"))
	origin := base.Scheme + "://" + base.Host
	u, err := url.Parse(returnTo)
	if err == nil && ssoReturnToOK(u, publicBaseURL) {
		q := u.Query()
		q.Set("token", linkToken)
		return origin + "/v1/sso/hop?" + q.Encode()
	}
	if err == nil && connectReturnToOK(u, publicBaseURL) {
		q := u.Query()
		q.Set("token", linkToken)
		return origin + "/oauth/authorize?" + q.Encode()
	}
	if err == nil && dashboardReturnToOK(u, publicBaseURL) {
		q := u.Query()
		q.Set("token", linkToken)
		return origin + "/?" + q.Encode()
	}
	// Unreachable for a return_to that passed SanitizeReturnTo; a token
	// without a nonce cannot be redeemed anyway.
	return origin + "/"
}

func isSchemeDefaultPort(scheme, port string) bool {
	return (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

func isRejectedPlatformHost(host, siteDomain, contentHost, publicBaseHostName string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	contentHost = strings.ToLower(strings.TrimSpace(contentHost))
	publicBaseHostName = strings.ToLower(strings.TrimSpace(publicBaseHostName))
	if host == siteDomain || host == "www."+siteDomain || (publicBaseHostName != "" && host == publicBaseHostName) {
		return true
	}
	if contentHost != "" && host == contentHost {
		return true
	}
	// Legacy <name>.<siteDomain> host.
	if siteDomain != "" && strings.HasSuffix(host, "."+siteDomain) {
		return true
	}
	return false
}

func (h *OAuthHandler) proveCustomDomainControl(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	target := strings.ToLower(strings.TrimSuffix(h.Cfg.CNAMETarget, "."))

	if h.Cfg.CustomDomainIP != "" && appsites.IsApexDomain(host) {
		ips, err := h.LookupIP(ctx, host)
		if err != nil {
			return err
		}
		if h.ipsAreOurs(ips) {
			return nil
		}
		return errInvalidReturnTo
	}

	if target != "" {
		canon, err := h.LookupCNAME(ctx, host)
		if err == nil {
			canon = strings.ToLower(strings.TrimSuffix(canon, "."))
			if canon == target {
				return nil
			}
		}
	}
	if h.Cfg.CustomDomainIP != "" {
		ips, err := h.LookupIP(ctx, host)
		if err != nil {
			return err
		}
		if h.ipsAreOurs(ips) {
			return nil
		}
	}
	return errInvalidReturnTo
}

// ipsAreOurs reports whether a lookup includes CUSTOM_DOMAIN_IP or one of the
// earlier addresses in CUSTOM_DOMAIN_IP_ALSO.
func (h *OAuthHandler) ipsAreOurs(ips []net.IP) bool {
	for _, want := range append([]string{h.Cfg.CustomDomainIP}, h.Cfg.CustomDomainIPAlso...) {
		if ipsContainIPv4(ips, want) {
			return true
		}
	}
	return false
}

func ipsContainIPv4(ips []net.IP, want string) bool {
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 != nil && v4.String() == want {
			return true
		}
	}
	return false
}

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func randomRaw(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func RandomHex(n int) (string, error) {
	b, err := randomRaw(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// siteSignInAllowed writes the refusal when site siteID takes no visitor
// sign-in now (AccessHandler.siteSignIn). Shared by the legacy provider start
// and the SSO start on the site host. Without Access no site signs anyone in.
func (h *OAuthHandler) siteSignInAllowed(w http.ResponseWriter, r *http.Request, siteID string) bool {
	if h.Access == nil {
		writeSiteLocked(w)
		return false
	}
	refusal, reason, err := h.Access.siteSignIn(r, siteID)
	switch {
	case err != nil:
		httpx.WriteInternal(w)
	case refusal == signInOffline:
		web.WriteSiteOffline(w)
	case refusal == signInSuspended:
		web.WriteSiteSuspended(w, reason)
	case refusal == signInLocked:
		writeSiteLocked(w)
	default:
		return true
	}
	return false
}

func (h *OAuthHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
