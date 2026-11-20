package handler

// The Simple Host connector: an OAuth 2.1 authorization server and the remote
// MCP endpoint it protects, in this binary.
//
// A person adds https://<apex>/mcp once in ChatGPT, Claude or Grok. The app
// discovers this server (RFC 9728 → RFC 8414), registers itself (RFC 7591),
// and opens /oauth/authorize in a browser window. That page is the normal
// Simple Host sign-in (email code or Google) followed by one consent screen;
// "Allow" sends the app back with a single-use code, which it trades (with its
// PKCE verifier) for an access token and a rotating refresh token. Every later
// chat presents the access token on /mcp and is already signed in.
//
// The same tokens are accepted on the REST /v1 API (BearerAuth), so a ChatGPT
// GPT Action can call the API per person. A token is exchanged in process for
// an internal credential, and the REST layer does the rest, so nothing here
// restates a permission rule. On an ordinary instance that credential is the
// person's own. On the hosted hackathon platform a new grant is personal;
// a team site is selected separately and checked against live membership.
//
// Token audiences (RFC 8707): a token requested for <apex>/mcp works only at
// /mcp; a token requested with no resource, or for the apex itself, is for
// this whole server (/v1 and /mcp). Both are the same person and the same
// server, so neither can be replayed anywhere else.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

const (
	oauthCodeTTL         = 60 * time.Second
	oauthConsentTTL      = 30 * time.Minute
	oauthMaxRedirectURIs = 10

	// Prefixes make a leaked token recognisable to secret scanners and to a
	// person reading a log, and let /v1 tell our token from a misplaced key.
	prefixAccess  = "shat_"
	prefixRefresh = "shrt_"
	prefixCode    = "shac_"
	prefixClient  = "shc_"
	prefixSecret  = "shcs_"
)

// ConnectorHandler serves the OAuth authorization server and /mcp.
type ConnectorHandler struct {
	sweepWorkerOnce sync.Once
	sweepWorker     *connectorSweepWorker
	httpCaches      httpx.Caches
	ed              *edition.Edition
	limitSet
	hostedEvents bool
	database     *sql.DB
	issuer       string // e.g. https://simple-host.app
	mcpResource  string // issuer + "/mcp"
	adminAPIKey  string
	mcp          *mcp.Server
	mcpConfig    mcp.Config
	upstream     http.Handler
	skillVersion string

	// consentKey signs the CSRF token on the consent screen. Per process: a
	// restart invalidates consent pages that are open, which only means
	// clicking Allow again.
	consentKey []byte

	// reviewer is the plugin-directory reviewer's password sign-in; nil
	// (off) unless the operator configures it. See reviewer.go.
	reviewer *reviewerSignIn

	// alerts emails the owner when an app is connected (signin_alert.go).
	alerts *SignInAlerts

	registerLimiter  *httpx.RateLimiter
	authorizeLimiter *httpx.RateLimiter
	tokenLimiter     *httpx.RateLimiter
	uploadLimiter    *httpx.RateLimiter

	now func() time.Time
	// teamConnections is on when the product has team connections: a new
	// grant is personal and selects a team (SetTeamConnections).
	teamConnections bool

	// assets is the asset/chrome pipeline for the consent screen. Read it through pipeline().
	assets     *assetPipeline
	assetsOnce sync.Once
}

// pipeline is h's asset pipeline. A bare &ConnectorHandler{} (tests) gets one
// for its (possibly nil, so Host) edition on first use.
func (h *ConnectorHandler) pipeline() *assetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = newAssetPipeline(h.ed, h.issuer)
		}
	})
	return h.assets
}

// NewConnectorHandlerForTest builds a minimal *ConnectorHandler for
// discovery-only tests: the issuer and edition policy, with calls
// served through mcpServer directly rather than building its own
// internally, so a test can compare its discovery documents against that
// exact *mcp.Server instance. Test hook (cleanup 3.2 slice 3): the moved
// hack_discovery_test.go built this as a bare composite literal
// (issuer/mcp/hostedEvents are unexported fields) before the move.
func NewConnectorHandlerForTest(issuer string, mcpServer *mcp.Server, ed *edition.Edition) *ConnectorHandler {
	return &ConnectorHandler{issuer: issuer, mcp: mcpServer, ed: ed, hostedEvents: ed.Routes().HostedEvents}
}

// MCPUpstreamForTest exposes the exact handler configured for MCP tool calls so
// product wiring tests can exercise it with visitor-session credentials.
func (h *ConnectorHandler) MCPUpstreamForTest() http.Handler {
	return h.mcpConfig.Upstream
}

// NewConnectorHandler builds the connector. upstream is the application mux
// behind the key scope gate (auth.ScopeGate): MCP tool calls are served into
// it in process, so a deploy-only key's tools meet the same route table.
func NewConnectorHandler(database *sql.DB, publicBaseURL, adminAPIKey, siteDomain, contentHost, skillVersion string, upstream http.Handler, hostedEvents bool, editions ...*edition.Edition) *ConnectorHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	issuer := strings.TrimRight(publicBaseURL, "/")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("connector: no randomness: " + err.Error())
	}
	apiHost := publicBaseHost(publicBaseURL)
	if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
		apiHost = u.Host
	}
	if apiHost == "" {
		apiHost = siteDomain
	}
	lim := ed.Limits()
	h := &ConnectorHandler{
		hostedEvents: hostedEvents,
		ed:           ed,
		limitSet:     newLimitSet(lim),
		database:     database,
		issuer:       issuer,
		mcpResource:  issuer + "/mcp",
		adminAPIKey:  adminAPIKey,
		consentKey:   key,
		// Registration: a chat app registers once per install, so a handful
		// an hour per address is plenty. Authorize and token: one person's
		// sign-in or a client's refresh loop, with room for retries.
		registerLimiter:  newRateLimiterFor(lim.RateOAuthRegister),
		authorizeLimiter: newRateLimiterFor(lim.RateOAuthAuthorize),
		tokenLimiter:     newRateLimiterFor(lim.RateOAuthToken),
		uploadLimiter:    newRateLimiterFor(lim.RateOAuthToken),
		now:              time.Now,
	}
	h.httpCaches.Rate("http.connector.registerLimiter", h.registerLimiter, 10*time.Minute, 2*time.Hour)
	h.httpCaches.Rate("http.connector.authorizeLimiter", h.authorizeLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.connector.tokenLimiter", h.tokenLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.connector.uploadLimiter", h.uploadLimiter, 10*time.Minute, 30*time.Minute)
	h.upstream = upstream
	h.skillVersion = skillVersion
	mcpSnapshot := mcpLimits(lim)
	h.mcpConfig = mcp.Config{
		Limits:             &mcpSnapshot,
		AnalyticsProjectID: strings.TrimSpace(os.Getenv("AGENTCAT_PROJECT_ID")),
		AnalyticsEnv:       analyticsEnv(issuer),
		AnalyticsPepper:    analyticsPepper(adminAPIKey),
		Upstream:           upstream,
		APIHost:            apiHost,
		ContentOrigin:      "https://" + contentHost,
		SiteBases:          []string{siteDomain},
		SkillVersion:       skillVersion,
		// This install's contact in hints (auth.SetSupportContact runs first).
		SupportContact: auth.SupportContact,
		ServerName:     "simple-host",
		Version:        skillVersion,
		// An inline deploy carries the whole site in one message: the per-site
		// limit, plus JSON/base64 overhead. The largest any account has
		// (MAX_ARCHIVE_MB_OVERRIDES), since the account is not known yet; the
		// deploy route then holds each account to its own.
		MaxBodyBytes: sites.JSONBodyLimit(h.largestSiteLimit()),
		// host/mcp is a leaf package (check-layering.sh) and may not
		// import host/papermetrics directly; this is the same hook it
		// used to call inline from host/mcp/tools.go.
		MutationResult: func(ctx context.Context, method, path string, status int) {
			paperRecorder(ctx).MutationResult(ctx, method, path, status)
		},
		// main sets it from the server's Turnstile keys (OfferSpamCheck).
		SelfHosted: siteDomain != canonicalSiteDomain,
		// Teams are offered where accounts own their own sites.
		Teams: ed.Identity().PersonalSites,
	}
	// Another Simple Host server names itself in the connector's text, with
	// the rewriter its llms.txt and Get started use (SetInstanceHosts runs
	// first). Simple Hack rewrites its own simple-hack.app mentions
	// separately (Contribution.Rewrite); this pass covers the
	// site-management tools' simple-host.app mentions (list_sites,
	// create_site, connect_domain, ...), which a Hack edition's MCP surface
	// serves too (a team's Website tab publishes through the same site
	// infrastructure) -- so it must run for a self-hosted Hack instance,
	// not only a self-hosted Host one. Gated on genuinely self-hosted
	// (neither hosted domain), not just !hostedEvents, so the real hosted
	// simple-hack.app (hostedEvents=true, siteDomain != canonicalSiteDomain
	// since that constant is Host-specific) keeps its own text unchanged.
	if instanceHosts != nil && "https://"+siteDomain != h.ed.Presentation().CanonicalBaseURL {
		h.mcpConfig.Rewrite = instanceHosts.r.Replace
	}
	h.mcp = mcp.NewServer(h.mcpConfig)
	return h
}

// SetVisitorReferences shares the site handler's visitor reference key
// (derived from PASSCODE_ENC_KEY, else the process-local export key).
// Call once at startup, before serving.
func (h *ConnectorHandler) SetVisitorReferences(sites *AccessHandler) {
	h.mcpConfig.VisitorRefs = mcp.NewVisitorReferences(sites.visitorRefKey())
	h.mcp = mcp.NewServer(h.mcpConfig)
}

// SetPreviewBases follows the same served bases as the site's URL routing.
// Call once at startup, before serving.
func (h *ConnectorHandler) SetPreviewBases(bases []string) {
	h.mcpConfig.SiteBases = append([]string(nil), bases...)
	h.mcp = mcp.NewServer(h.mcpConfig)
}

// OfferSpamCheck tells the connector whether this server runs the spam check
// (SiteHandler.SpamCheckEnabled), so its tools offer auth.checked
// only where the server takes it. Call once at startup, before serving.
func (h *ConnectorHandler) OfferSpamCheck(on bool) {
	h.mcpConfig.NoSpamCheck = !on
	h.mcp = mcp.NewServer(h.mcpConfig)
}

// SetTools installs a product's AI tools. Call once at startup, before
// serving; the routes Register added read the server at request time.
func (h *ConnectorHandler) SetTools(product mcp.Contribution) {
	h.mcpConfig.Contribution = &product
	h.mcp = mcp.NewServer(h.mcpConfig)
}

// SetEventsBackend enables MCP Events. Call once at startup, before serving.
func (h *ConnectorHandler) SetEventsBackend(b mcp.EventsBackend) {
	h.mcpConfig.Events = b
	h.mcp = mcp.NewServer(h.mcpConfig)
}

// StartSweep removes expired codes and tokens, abandoned registrations, and
// API keys that stopped working more than 30 days ago.
type connectorSweepWorker struct {
	startOnce sync.Once
	*workers.Runner
	h       *ConnectorHandler
	tracker *workers.Tracker
	every   time.Duration
}

func (h *ConnectorHandler) SweepWorker(every time.Duration) workers.Owner {
	h.sweepWorkerOnce.Do(func() {
		t := workers.NewTracker("accounts.connector_sweep")
		h.sweepWorker = &connectorSweepWorker{Runner: workers.NewRunner(t), h: h, tracker: t, every: every}
	})
	return h.sweepWorker
}
func (w *connectorSweepWorker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		h := w.h
		// The five OAuth limiters are evicted by h.httpCaches; this owner only sweeps.
		w.Go(ctx, func(ctx context.Context) {
			timer := time.NewTimer(45 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if ctx.Err() != nil {
					return
				}
				func() {
					w.tracker.Attempt()
					ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					n, err := h.accountsSvc().SweepOAuth(ctx, h.limits.OAuthUnusedClientAge)
					if err != nil {
						w.tracker.Fail("sweep OAuth", err)
					}
					if n > 0 {
						w.tracker.Progress()
					}
					if n, err := h.accountsSvc().PurgeExpiredAPIKeys(ctx, h.limits.KeyIdleExpiry); err != nil {
						w.tracker.Fail("purge expired API keys", err)
					} else if n > 0 {
						w.tracker.Progress()
						log.Printf("api key sweep: removed %d keys expired over 30 days", n)
					}
				}()
				timer.Reset(w.every)
			}
		})
	})
}
func (h *ConnectorHandler) StartSweep(every time.Duration) {
	h.SweepWorker(every).Start(context.Background())
}

func (h *ConnectorHandler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	RegisterAgentCard(mux, h.issuer, func() *mcp.Server { return h.mcp }, h.hostedEvents)
	RegisterMCPServerCard(mux, h.issuer, h.hostedEvents, h.ed.Presentation())
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.authorizationServerMetadata)
	// Some clients derive the metadata address by inserting the MCP path.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", h.authorizationServerMetadata)
	mux.HandleFunc("GET "+museClientPath, h.museClientMetadata)

	mux.Handle("POST /oauth/register", rateLimitByIP(h.registerLimiter, http.HandlerFunc(h.register)))
	mux.Handle("GET /oauth/authorize", rateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.authorize)))
	mux.Handle("POST /oauth/authorize/decision", rateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.decide)))
	// Answers 404 unless the reviewer account is configured.
	// The one other route that answers with an API key: the app's own address only.
	mux.Handle("POST /oauth/reviewer-signin", accountAuthOnly(h.issuer, http.HandlerFunc(h.reviewerSignInHandler)))
	mux.Handle("POST /oauth/token", rateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.token)))
	mux.Handle("POST /oauth/revoke", rateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.revoke)))

	// POST carries every message. GET and DELETE are answered 405 by the MCP
	// server (no standalone stream, no sessions), after authentication, so an
	// unauthenticated probe of any method learns where to sign in.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		mux.HandleFunc(m+" /mcp", h.serveMCP)
	}

	photoLinks := photosHostOnly(!h.hostedEvents, accountAuthOnly(h.issuer, rateLimitByIP(h.uploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServePhotoLink(w, r) }))))
	mux.Handle("GET /v1/photo-links", photoLinks)
	mux.Handle("POST /v1/photo-links", photoLinks)
	mux.Handle("PUT /v1/uploads/{token}", rateLimitByIP(h.uploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServeUpload(w, r) })))
	mux.Handle("POST /v1/uploads/{token}", rateLimitByIP(h.uploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServeUpload(w, r) })))

	mux.Handle("GET /v1/me/connections", authMiddleware(http.HandlerFunc(h.listConnections)))
	mux.Handle("DELETE /v1/me/connections/{client_id}", authMiddleware(http.HandlerFunc(h.deleteConnection)))
}

// ---- metadata ---------------------------------------------------------------

func (h *ConnectorHandler) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resource := h.mcpResource
	if r.URL.Path == "/.well-known/oauth-protected-resource" {
		// The root document describes the whole server: tokens for it also
		// work on /v1. MCP clients read the /mcp-suffixed one.
		resource = h.issuer
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{h.issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         h.scopesSupported(),
		"resource_name":            "Simple Host",
		"resource_documentation":   h.issuer + "/llms.txt",
	})
}

func (h *ConnectorHandler) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	authMethods := []string{"none", "client_secret_post", "client_secret_basic"}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         h.issuer,
		"authorization_endpoint":                         h.issuer + "/oauth/authorize",
		"token_endpoint":                                 h.issuer + "/oauth/token",
		"registration_endpoint":                          h.issuer + "/oauth/register",
		"revocation_endpoint":                            h.issuer + "/oauth/revoke",
		"scopes_supported":                               h.scopesSupported(),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          authMethods,
		"revocation_endpoint_auth_methods_supported":     authMethods,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          h.issuer + "/llms.txt",
	})
}

// ---- helpers ------------------------------------------------------------------

func randomToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("connector: no randomness: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret is how every connector secret is stored and looked up. The
// secrets are 256-bit random values, so a fast hash is the right one: there is
// nothing to brute-force.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	body := map[string]string{"error": code}
	if description != "" {
		body["error_description"] = description
	}
	writeJSON(w, status, body)
}

// validRedirectURI is the registration rule (RFC 8252, MCP authorization):
// https anywhere; http only on a loopback address, for an app on the
// person's own machine; or an app's own URL scheme (cursor://, vscode://,
// com.example.app:/), which only that installed app answers. No fragment, no
// credentials in the URL. Every client is held to PKCE, which is what keeps
// a code sent to a shared or claimed scheme useless to anyone else.
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > 2000 || strings.ContainsAny(raw, " \t\r\n#\\") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return validAppScheme(u)
}

// blockedRedirectSchemes are schemes a browser or the operating system
// handles itself (scripts, local files, other network protocols, settings
// and troubleshooting tools), so they are never an app's sign-in return.
var blockedRedirectSchemes = map[string]bool{
	"javascript": true, "vbscript": true, "data": true, "blob": true, "file": true,
	"filesystem": true, "about": true, "view-source": true, "resource": true,
	"chrome": true, "edge": true, "opera": true, "brave": true, "intent": true,
	"jar": true, "ftp": true, "ftps": true, "sftp": true, "ws": true, "wss": true,
	"ssh": true, "telnet": true, "smb": true, "nfs": true, "afp": true, "ldap": true,
	"mailto": true, "tel": true, "sms": true, "callto": true, "facetime": true,
	"search": true, "search-ms": true, "shell": true, "hcp": true, "help": true,
	"mk": true, "its": true, "res": true, "urn": true, "smsto": true, "mms": true,
	"facetime-audio": true, "skype": true, "sip": true, "steam": true, "market": true,
	"chrome-extension": true, "moz-extension": true, "safari-extension": true,
	"webcal": true, "feed": true, "itunes": true, "content": true, "irc": true, "magnet": true, "git": true, "svn": true,
}

// blockedRedirectSchemePrefixes cover families of system handlers: Windows
// ms-* tools, Apple's installers and settings (itms-services installs an app
// from a URL), and the operating system's own x-apple handlers.
var blockedRedirectSchemePrefixes = []string{"ms-", "itms", "x-apple", "apple", "microsoft-", "windows", "android", "google-"}

// validAppScheme accepts a private-use scheme (RFC 8252 section 7.1):
// cursor://anysphere.cursor-mcp/oauth/callback, or the reverse-domain
// com.example.app:/oauth2redirect. The address carries no query, so a
// registration cannot smuggle parameters to whatever handles the scheme
// (authorize errors go back before anyone signs in), and a hostless one must
// use a reverse-domain scheme, as RFC 8252 asks.
func validAppScheme(u *url.URL) bool {
	scheme := u.Scheme
	if len(scheme) < 2 || len(scheme) > 64 || blockedRedirectSchemes[scheme] || u.RawQuery != "" || u.ForceQuery {
		return false
	}
	for _, p := range blockedRedirectSchemePrefixes {
		if strings.HasPrefix(scheme, p) {
			return false
		}
	}
	if u.Host != "" {
		return u.Port() == "" && !strings.ContainsAny(u.Host, "[]%")
	}
	return strings.Contains(scheme, ".") && strings.HasPrefix(u.Path, "/")
}

// loopbackPortMatch lets a loopback redirect come back on any port (RFC 8252
// section 7.3): an app on the person's machine picks a free port each time.
// Everything else about the address must match what was registered.
func loopbackPortMatch(registered, presented string) bool {
	reg, err1 := url.Parse(registered)
	pre, err2 := url.Parse(presented)
	if err1 != nil || err2 != nil || reg.Scheme != "http" || pre.Scheme != "http" || !validRedirectURI(presented) {
		return false
	}
	host := reg.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	return host == pre.Hostname() && reg.EscapedPath() == pre.EscapedPath() && reg.RawQuery == pre.RawQuery
}

// redirectTarget is how the consent page names where the person goes back
// to: the host for a web address, and the scheme too for an app's own
// scheme, so "cursor://anysphere.cursor-mcp" reads as the Cursor app.
func redirectTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	switch u.Scheme {
	case "https", "http":
		return u.Hostname()
	}
	if u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + ":"
}

func validPKCEValue(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}

// pkceS256 reports whether verifier matches challenge, in constant time.
func pkceS256(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// normalizeResource maps a requested resource to the audience stored on the
// grant: this server's /mcp, or the server as a whole. Anything else is not a
// resource this server serves.
func (h *ConnectorHandler) normalizeResource(raw string) (string, bool) {
	if raw == "" {
		return h.issuer, true
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Fragment != "" || u.RawQuery != "" {
		return "", false
	}
	trimmed := strings.TrimRight(raw, "/")
	switch {
	case strings.EqualFold(trimmed, h.mcpResource):
		return h.mcpResource, true
	case strings.EqualFold(h.issuer, "https://simple-host.app") && strings.EqualFold(trimmed, "https://mcp.simple-host.app/mcp"):
		return h.mcpResource, true
	case strings.EqualFold(trimmed, h.issuer):
		return h.issuer, true
	}
	return "", false
}

// ---- dynamic client registration (RFC 7591) ------------------------------------

func (h *ConnectorHandler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON client metadata document")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > oauthMaxRedirectURIs {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "between 1 and 10 redirect_uris are required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			log.Printf("connector: register refused redirect_uri=%q", truncate(u, 300))
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, http on a loopback address, or an app's own scheme, with no fragment")
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "client_secret_basic" // RFC 7591 §2 default
	}
	if method != "none" && method != "client_secret_post" && method != "client_secret_basic" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic")
		return
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "only the authorization_code and refresh_token grants are supported")
			return
		}
	}
	for _, t := range req.ResponseTypes {
		if t != "code" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "only the code response type is supported")
			return
		}
	}

	client := model.OAuthClient{
		ClientID:                randomToken(prefixClient, 16),
		Name:                    httpx.CleanClientName(req.ClientName),
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: method,
		PKCERequired:            true,
		Dynamic:                 true,
	}
	var secret string
	if method != "none" {
		secret = randomToken(prefixSecret, 32)
		client.SecretHash = sql.NullString{String: hashSecret(secret), Valid: true}
	}
	if err := h.accountsSvc().InsertOAuthClient(r.Context(), client); err != nil {
		log.Printf("connector: register client: %v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	resp := map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        h.now().Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
		"scope":                      auth.OAuthScope,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, resp)
}

// CreateOperatorClient registers a confidential client by hand (the
// `simple-host oauth-client create` command): a ChatGPT GPT Action, which is
// configured with a fixed client ID and secret rather than registering itself.
// The secret is returned once and stored only as a hash. pkceRequired=false is
// allowed only here, for platforms that cannot send PKCE.
func CreateOperatorClient(ctx context.Context, database *sql.DB, name string, redirectURIs []string, pkceRequired bool) (string, string, error) {
	for _, u := range redirectURIs {
		if !validRedirectURI(u) {
			return "", "", fmt.Errorf("invalid redirect URI %q: must be https (or http on localhost), with no fragment", u)
		}
	}
	if len(redirectURIs) > oauthMaxRedirectURIs {
		return "", "", fmt.Errorf("at most %d redirect URIs", oauthMaxRedirectURIs)
	}
	secret := randomToken(prefixSecret, 32)
	client := model.OAuthClient{
		ClientID:                randomToken(prefixClient, 16),
		SecretHash:              sql.NullString{String: hashSecret(secret), Valid: true},
		Name:                    httpx.CleanClientName(name),
		RedirectURIs:            append([]string{}, redirectURIs...),
		TokenEndpointAuthMethod: "client_secret_post",
		PKCERequired:            pkceRequired,
		Dynamic:                 false,
	}
	if client.RedirectURIs == nil {
		client.RedirectURIs = []string{}
	}
	if err := accounts.New(database).InsertOAuthClient(ctx, client); err != nil {
		return "", "", err
	}
	return client.ClientID, secret, nil
}

// SetOperatorClientRedirects replaces an operator client's redirect URIs.
func SetOperatorClientRedirects(ctx context.Context, database *sql.DB, clientID string, redirectURIs []string) error {
	for _, u := range redirectURIs {
		if !validRedirectURI(u) {
			return fmt.Errorf("invalid redirect URI %q: must be https (or http on localhost), with no fragment", u)
		}
	}
	if len(redirectURIs) > oauthMaxRedirectURIs {
		return fmt.Errorf("at most %d redirect URIs", oauthMaxRedirectURIs)
	}
	return accounts.New(database).SetOAuthClientRedirectURIs(ctx, clientID, redirectURIs)
}

// ---- Meta Muse ---------------------------------------------------------------------

// Meta Muse neither registers itself (RFC 7591) nor publishes client
// metadata. It also cannot be trusted to carry a URL through its Client ID
// field unmangled (a phone keyboard has appended ".com" to it, and the owner
// has typed an email instead), so the same pre-registered public client is
// reachable under a short id, meta-muse, which we show everywhere; the
// Client ID Metadata Document at museClientPath still works too, since its
// own URL must remain its client_id (that is the point of the document).
// Both are pre-registered (EnsureBuiltinClients), held to PKCE like any
// other public client. Fetching arbitrary client ID URLs is not supported,
// so discovery does not advertise client_id_metadata_document_supported;
// clients that can register keep using /oauth/register.
const (
	museClientPath    = "/.well-known/muse-client.json"
	museShortClientID = "meta-muse"
	museClientName    = "Meta Muse"
	museRedirectURI   = "https://agent.meta.ai/api/hatch/oauth/callback"
)

func (h *ConnectorHandler) museClientID() string { return h.issuer + museClientPath }

// EnsureBuiltinClients creates or refreshes the clients the server defines
// itself. Run at boot; idempotent.
func (h *ConnectorHandler) EnsureBuiltinClients(ctx context.Context) error {
	if err := h.accountsSvc().UpsertBuiltinOAuthClient(ctx, h.museClientID(), museClientName, []string{museRedirectURI}); err != nil {
		return err
	}
	return h.accountsSvc().UpsertBuiltinOAuthClient(ctx, museShortClientID, museClientName, []string{museRedirectURI})
}

// museKeyboardMangled reports a client ID that is one of ours changed on the
// way in: the document URL with something appended (".com" seen live), or
// meta-muse capitalised or padded by autocorrect. Only the help text uses
// this; the exact ids stay the only ones accepted.
func museKeyboardMangled(clientID string) bool {
	lower := strings.ToLower(clientID)
	return strings.Contains(lower, "muse-client") || strings.TrimSpace(lower) == museShortClientID
}

func (h *ConnectorHandler) museClientMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"client_id":                  h.museClientID(),
		"client_name":                museClientName,
		"redirect_uris":              []string{museRedirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      auth.OAuthScope,
	})
}

// clientIDShape describes an unknown client ID for the log without
// recording what a person may have typed into it.
func clientIDShape(id string) string {
	switch {
	case strings.HasPrefix(id, prefixClient):
		return "expired registration"
	case strings.Contains(id, "://"):
		if u, err := url.Parse(id); err == nil {
			return "url host=" + truncate(u.Host, 100)
		}
		return "url"
	case strings.Contains(id, "@"):
		return "email address"
	}
	return "other, " + strconv.Itoa(len(id)) + " characters"
}

// ---- connected apps --------------------------------------------------------------------

// connectionDevice is the hint kept with a connection so two of the same app
// can be told apart: the consent page's browser, summarised ("Chrome on
// macOS"), or "" when it cannot be told. Never the user agent itself or an
// address.
func connectionDevice(userAgent string) string {
	d := summarizeUserAgent(userAgent)
	if d == "an unknown browser or app" {
		return ""
	}
	if len(d) > 60 {
		d = d[:60]
	}
	return d
}

func (h *ConnectorHandler) listConnections(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	conns, err := h.accountsSvc().ListOAuthConnections(r.Context(), user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		item := map[string]any{
			"client_id":         c.ClientID,
			"name":              c.ClientName,
			"connected_at":      c.ConnectedAt,
			"last_used_at":      c.LastUsedAt,
			"last_visitor_site": c.LastVisitorSite,
			"device":            c.Device,
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (h *ConnectorHandler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	n, err := h.accountsSvc().DeleteOAuthGrantsForClient(r.Context(), user.ID, r.PathValue("client_id"))
	if err != nil {
		writeInternal(w)
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no connected app with that id"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// truncate cuts s to at most n bytes, for log lines.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// analyticsPepper keys the usage events' account hash to this server's admin
// key, which never leaves it.
func analyticsPepper(adminAPIKey string) string {
	sum := sha256.Sum256([]byte("agentcat-pepper:" + adminAPIKey))
	return hex.EncodeToString(sum[:])
}

// analyticsEnv tags usage events so test instances can be filtered out.
func analyticsEnv(issuer string) string {
	if issuer == "https://simple-host.app" {
		return "prod"
	}
	return "dev"
}

func (h *ConnectorHandler) accountsSvc() *accounts.Service { return accounts.New(h.database) }
func (h *ConnectorHandler) connectorSvc() *accounts.Connector {
	return accounts.NewConnector(h.database, h.now, h.normalizeResource, h.teamConnections, h.oauthAccessTTL(), h.oauthRefreshTTL())
}
func (h *ConnectorHandler) writeConnectorTokens(w http.ResponseWriter, reply accounts.ConnectorTokens, err error) {
	if err != nil {
		e := err.(*accounts.ConnectorError)
		status := http.StatusBadRequest
		if e.Internal {
			status = http.StatusInternalServerError
		}
		oauthError(w, status, e.Code, e.Description)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{"access_token": reply.AccessToken, "token_type": reply.TokenType, "expires_in": reply.ExpiresIn, "refresh_token": reply.RefreshToken, "scope": reply.Scope})
}
