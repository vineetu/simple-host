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
	"bytes"
	"context"
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
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/accounts"
	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/edition"
	httpx "github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/workers"
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
	httpCaches      HTTPCaches
	ed              *edition.Edition
	limitSet
	hostedEvents bool
	database     *sql.DB
	issuer       string // e.g. https://simple-host.app
	mcpResource  string // issuer + "/mcp"
	// brainResource is issuer + "/brain/mcp" when the owner's knowledge
	// server is on (EnableBrain), else "".
	brainResource string
	adminAPIKey   string
	mcp           *mcp.Server
	mcpConfig     mcp.Config
	imports       *appsites.ImportManager
	upstream      http.Handler
	skillVersion  string

	// consentKey signs the CSRF token on the consent screen. Per process: a
	// restart invalidates consent pages that are open, which only means
	// clicking Allow again.
	consentKey []byte

	// reviewer is the plugin-directory reviewer's password sign-in; nil
	// (off) unless the operator configures it. See reviewer.go.
	reviewer *reviewerSignIn

	// alerts emails the owner when an app is connected (signin_alert.go).
	alerts *SignInAlerts

	registerLimiter     *rateLimiter
	authorizeLimiter    *rateLimiter
	tokenLimiter        *rateLimiter
	uploadLimiter       *rateLimiter
	importStatusLimiter *rateLimiter

	now func() time.Time
	// hackTeams gates hosted hackathon grants and tools (EVENTS=hosted).
	hackTeams bool

	// assets is the asset/chrome pipeline for ed and issuer (the consent
	// screen and import page); read it through pipeline().
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
// discovery-only tests: just the issuer, whether hosted events are on, and
// serving through mcpServer directly rather than building its own
// internally, so a test can compare its discovery documents against that
// exact *mcp.Server instance. Test hook (cleanup 3.2 slice 3): the moved
// hack_discovery_test.go built this as a bare composite literal
// (issuer/mcp/hostedEvents are unexported fields) before the move.
func NewConnectorHandlerForTest(issuer string, mcpServer *mcp.Server, hostedEvents bool) *ConnectorHandler {
	return &ConnectorHandler{issuer: issuer, mcp: mcpServer, hostedEvents: hostedEvents}
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
		registerLimiter:     newRateLimiterFor(lim.RateOAuthRegister),
		authorizeLimiter:    newRateLimiterFor(lim.RateOAuthAuthorize),
		tokenLimiter:        newRateLimiterFor(lim.RateOAuthToken),
		uploadLimiter:       newRateLimiterFor(lim.RateOAuthToken),
		importStatusLimiter: newRateLimiter(60, 1),
		now:                 time.Now,
	}
	h.httpCaches.rate("http.connector.registerLimiter", h.registerLimiter, 10*time.Minute, 2*time.Hour)
	h.httpCaches.rate("http.connector.authorizeLimiter", h.authorizeLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.connector.tokenLimiter", h.tokenLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.connector.uploadLimiter", h.uploadLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.connector.importStatusLimiter", h.importStatusLimiter, 10*time.Minute, 30*time.Minute)
	h.imports = appsites.NewImportManager(lim.ImportMaxConcurrent)
	h.upstream = upstream
	h.skillVersion = skillVersion
	mcpSnapshot := mcpLimits(lim)
	h.mcpConfig = mcp.Config{
		Limits:              &mcpSnapshot,
		AnalyticsProjectID:  strings.TrimSpace(os.Getenv("AGENTCAT_PROJECT_ID")),
		AnalyticsEnv:        analyticsEnv(issuer),
		AnalyticsPepper:     analyticsPepper(adminAPIKey),
		ImportLinkTTL:       lim.ImportLinkTTL,
		ImportCreated:       func(link mcp.ImportLink) error { return h.imports.Created(importJobLink(link)) },
		ImportReceiving:     h.imports.Receiving,
		ImportReport:        h.imports.Report,
		ValidateImportNames: true,
		Upstream:            upstream,
		APIHost:             apiHost,
		ContentOrigin:       "https://" + contentHost,
		SiteBases:           []string{siteDomain},
		SkillVersion:        skillVersion,
		// This install's contact in hints (auth.SetSupportContact runs first).
		SupportContact: auth.SupportContact,
		ServerName:     "simple-host",
		Version:        skillVersion,
		// An inline deploy carries the whole site in one message: the per-site
		// limit, plus JSON/base64 overhead. The largest any account has
		// (MAX_ARCHIVE_MB_OVERRIDES), since the account is not known yet; the
		// deploy route then holds each account to its own.
		MaxBodyBytes:   h.largestSiteLimit()*4/3 + (1 << 20),
		SelectHackTeam: h.selectHackTeam,
		// internal/mcp is a leaf package (check-layering.sh) and may not
		// import internal/papermetrics directly; this is the same hook it
		// used to call inline from internal/mcp/tools.go.
		MutationResultHook: func(ctx context.Context, method, path string, status int) {
			paperRecorder(ctx).MutationResult(ctx, method, path, status)
		},
		// main sets it from the server's Turnstile keys (OfferSpamCheck).
		SelfHosted: siteDomain != canonicalSiteDomain,
	}
	// Another Simple Host server names itself in the connector's text, with
	// the rewriter its llms.txt and Get started use (SetInstanceHosts runs
	// first). Simple Hack rewrites its own simple-hack.app mentions
	// separately (hackInstanceTools, server.go); this pass covers the
	// site-management tools' simple-host.app mentions (list_sites,
	// create_site, connect_domain, ...), which a Hack edition's MCP surface
	// serves too (a team's Website tab publishes through the same site
	// infrastructure) -- so it must run for a self-hosted Hack instance,
	// not only a self-hosted Host one. Gated on genuinely self-hosted
	// (neither hosted domain), not just !hostedEvents, so the real hosted
	// simple-hack.app (hostedEvents=true, siteDomain != canonicalSiteDomain
	// since that constant is Host-specific) keeps its own text unchanged.
	if instanceHosts != nil && siteDomain != "simple-hack.app" {
		h.mcpConfig.Rewrite = instanceHosts.r.Replace
	}
	h.mcp = mcp.NewServer(h.mcpConfig)
	return h
}

// SetVisitorReferences shares the site handler's visitor reference key
// (derived from PASSCODE_ENC_KEY, else the process-local export key).
// Call once at startup, before serving.
func (h *ConnectorHandler) SetVisitorReferences(sites *SiteHandler) {
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
// (SiteHandler.SpamCheckEnabled), so its tools offer the comments preset
// only where the server takes it. Call once at startup, before serving.
func (h *ConnectorHandler) OfferSpamCheck(on bool) {
	h.mcpConfig.NoSpamCheck = !on
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
	RegisterAgentCard(mux, h.issuer, h.mcp, h.hostedEvents)
	RegisterMCPServerCard(mux, h.issuer, h.hostedEvents)
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

	mux.Handle("GET /import/{token}", h.importOrigin(h.pipeline().adminUICSP(http.HandlerFunc(h.importPage))))
	mux.Handle("POST /v1/imports/{token}", h.importOrigin(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.receiveImport))))
	mux.Handle("GET /v1/imports/status/{import_id}", h.importOrigin(rateLimitByIP(h.importStatusLimiter, http.HandlerFunc(h.importStatus))))

	photoLinks := photosHostOnly(!h.hostedEvents, accountAuthOnly(h.issuer, rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.mcp.ServePhotoLink))))
	mux.Handle("GET /v1/photo-links", photoLinks)
	mux.Handle("POST /v1/photo-links", photoLinks)
	mux.Handle("PUT /v1/uploads/{token}", rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.mcp.ServeUpload)))
	mux.Handle("POST /v1/uploads/{token}", rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.mcp.ServeUpload)))

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
		"resource_documentation":   h.issuer + "/docs/",
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
		"service_documentation":                          h.issuer + "/docs/",
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
	case h.isBrainResource(trimmed):
		return h.brainResource, true
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

// ---- authorization endpoint ------------------------------------------------------

// authzRequest is a validated authorization request.
type authzRequest struct {
	Client        model.OAuthClient
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
}

// authzError is a failure of an authorization request. With redirect set, it
// is reported to the app at its (validated) redirect URI; without, the person
// is shown it, because the redirect URI itself could not be trusted.
type authzError struct {
	redirect    bool
	code        string
	description string
	// museHelp: the client ID was not one, most likely a person typing
	// something else into Meta Muse's Client ID box. The page says what to paste.
	museHelp bool
	// museTypo: the client ID is one of ours mangled by a keyboard or
	// autocorrect (museKeyboardMangled) rather than something unrelated typed
	// in by hand. The page names the cause.
	museTypo bool
}

func (h *ConnectorHandler) parseAuthorize(ctx context.Context, q url.Values) (authzRequest, *authzError) {
	var req authzRequest
	for k, v := range q {
		if len(v) > 1 && (k == "client_id" || k == "redirect_uri") {
			return req, &authzError{code: "invalid_request", description: "This sign-in link is not valid (a parameter is repeated)."}
		}
	}
	clientID := q.Get("client_id")
	if clientID == "" {
		return req, &authzError{code: "invalid_request", description: "This sign-in link is missing which app is asking. Go back to the app and try connecting again."}
	}
	client, err := h.accountsSvc().GetOAuthClient(ctx, clientID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("connector: get client: %v", err)
		}
		redirect := q.Get("redirect_uri")
		museHelp := redirect == museRedirectURI || !(strings.HasPrefix(clientID, prefixClient) || strings.Contains(clientID, "://"))
		// The redirect URI tells us which app it was; the client ID itself
		// may be anything the person typed (an email), so only its shape.
		log.Printf("connector: unknown client_id (%s) redirect_uri=%q", clientIDShape(clientID), truncate(redirect, 300))
		if museHelp {
			return req, &authzError{code: "invalid_client", museHelp: true, museTypo: museKeyboardMangled(clientID), description: "The Client ID entered in the app is not one Simple Host knows."}
		}
		return req, &authzError{code: "invalid_client", description: "The app that sent you here is not known to Simple Host. Go back to the app and try connecting again."}
	}
	redirectURI := q.Get("redirect_uri")
	matched := false
	for _, registered := range client.RedirectURIs {
		if redirectURI != "" && (subtle.ConstantTimeCompare([]byte(registered), []byte(redirectURI)) == 1 || loopbackPortMatch(registered, redirectURI)) {
			matched = true
			break
		}
	}
	if !matched {
		return req, &authzError{code: "invalid_request", description: "The app asked to send you back to an address it did not register, so Simple Host stopped here. Go back to the app and try again."}
	}
	req.Client, req.RedirectURI = client, redirectURI

	// From here on the redirect URI is trusted, and errors go back to the app.
	if len(q["state"]) == 1 && len(q.Get("state")) <= 2000 {
		req.State = q.Get("state")
	}
	for k, v := range q {
		if len(v) > 1 {
			return req, &authzError{redirect: true, code: "invalid_request", description: "parameter " + k + " is repeated"}
		}
	}
	// state first, so every error below carries it back to the app.
	req.State = q.Get("state")
	if len(req.State) > 2000 {
		req.State = ""
		return req, &authzError{redirect: true, code: "invalid_request", description: "state is too long"}
	}
	if q.Get("response_type") != "code" {
		return req, &authzError{redirect: true, code: "unsupported_response_type", description: "response_type must be code"}
	}
	challenge, method := q.Get("code_challenge"), q.Get("code_challenge_method")
	switch {
	case challenge == "" && method != "":
		return req, &authzError{redirect: true, code: "invalid_request", description: "code_challenge_method without code_challenge"}
	case challenge == "" && client.PKCERequired:
		return req, &authzError{redirect: true, code: "invalid_request", description: "PKCE is required: send code_challenge with code_challenge_method=S256"}
	case challenge != "" && method != "S256":
		return req, &authzError{redirect: true, code: "invalid_request", description: "only code_challenge_method=S256 is supported"}
	case challenge != "" && !validPKCEValue(challenge):
		return req, &authzError{redirect: true, code: "invalid_request", description: "code_challenge is malformed"}
	}
	req.CodeChallenge = challenge
	resource, ok := h.normalizeResource(q.Get("resource"))
	if !ok {
		return req, &authzError{redirect: true, code: "invalid_target", description: "resource must be " + h.mcpResource}
	}
	req.Resource = resource
	// scope: requested scopes are ignored rather than refused, so a client
	// that asks for something generic still reaches the consent page. What
	// is granted is decided there: "sites" on an ordinary instance; on the
	// hosted hackathon platform, "sites events" for one personal connection.
	return req, nil
}

// scopesSupported is the scope list published in discovery. Hosted adds
// events, the marker of an event-management grant. The challenge itself
// stays "sites": clients already ask for that, and the consent page chooses.
func (h *ConnectorHandler) scopesSupported() []string {
	if h.hackTeams {
		return []string{auth.OAuthScope, auth.HackEventsScope}
	}
	return []string{auth.OAuthScope}
}

// redirectWith builds the redirect back to the app, keeping any query the
// registered URI already had.
func (h *ConnectorHandler) redirectWith(redirectURI string, params map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("iss", h.issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *ConnectorHandler) errorRedirect(req authzRequest, e *authzError) string {
	return h.redirectWith(req.RedirectURI, map[string]string{"error": e.code, "error_description": e.description, "state": req.State})
}

// consentCSRF binds a consent decision to the page this server rendered for
// the same request, within oauthConsentTTL. The decision is also authenticated
// with the person's API key in a header, which a cross-site page cannot send.
func (h *ConnectorHandler) consentCSRF(req authzRequest, issued int64) string {
	mac := hmac.New(sha256.New, h.consentKey)
	for _, part := range []string{req.Client.ClientID, req.RedirectURI, req.CodeChallenge, req.State, req.Resource, strconv.FormatInt(issued, 10)} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return strconv.FormatInt(issued, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *ConnectorHandler) checkConsentCSRF(req authzRequest, token string) bool {
	issuedStr, _, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	issued, err := strconv.ParseInt(issuedStr, 10, 64)
	if err != nil {
		return false
	}
	age := h.now().Unix() - issued
	if age < 0 || age > int64(oauthConsentTTL/time.Second) {
		return false
	}
	return hmac.Equal([]byte(h.consentCSRF(req, issued)), []byte(token))
}

// consentHeaders: the consent screen is never framed (clickjacking an Allow
// button is the attack), never cached, and leaks nothing in a Referer.
// consentHeaders sets the consent page's own policy; nonce is the one its
// inline scripts were stamped with (the page reads the API key, so no
// 'unsafe-inline' for scripts).
func consentHeaders(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *ConnectorHandler) authorize(w http.ResponseWriter, r *http.Request) {
	req, aerr := h.parseAuthorize(r.Context(), r.URL.Query())
	if aerr != nil && aerr.redirect {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.errorRedirect(req, aerr), http.StatusFound)
		return
	}
	data := map[string]any{"ok": aerr == nil}
	status := http.StatusOK
	if aerr != nil {
		data["error"] = aerr.description
		if aerr.museHelp {
			data["muse_client_id"] = museShortClientID
			data["mcp_url"] = h.mcpResource
			if aerr.museTypo {
				data["muse_typo"] = true
			}
		}
		status = http.StatusBadRequest
	} else {
		data["client_name"] = req.Client.Name
		data["redirect_host"] = redirectTarget(req.RedirectURI)
		data["csrf"] = h.consentCSRF(req, h.now().Unix())
		if h.reviewer != nil {
			data["reviewer_signin"] = true
		}
		if h.hackTeams {
			data["hack"] = true
		}
		if h.brainResource != "" && req.Resource == h.brainResource {
			data["brain"] = true
		}
		// This install's own host, named on the sign-in step and the Allow
		// page on a self-hosted instance only (SIM connect-mcp rule 4):
		// hosted text already says Simple Host/Simple Hack and stays as it
		// is, so host/self_hosted are only emitted on self-hosted -- hosted's
		// consent HTML/JSON must stay byte-identical to the baseline.
		if h.pipeline().hostedOwner() == "" {
			data["self_hosted"] = true
			data["host"] = r.Host
		}
	}
	h.renderConsent(w, r, status, data)
}

func (h *ConnectorHandler) renderConsent(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	page, err := h.pipeline().chromePage("connect.html", h.pipeline().chromeDataFor(r, ""))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(nb[:])
	page = bytes.ReplaceAll(page, []byte("<script>"), []byte(`<script nonce="`+nonce+`">`))
	// json.Marshal escapes <, > and & so the data cannot close the script.
	payload, _ := json.Marshal(data)
	page = bytes.Replace(page, []byte("<!--sh:connect-data-->"),
		append(append([]byte(`<script type="application/json" id="connect-data">`), payload...), []byte(`</script>`)...), 1)
	consentHeaders(w, nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(page)
}

// SetHackTeams turns on hosted hackathon grants (EVENTS=hosted).
func (h *ConnectorHandler) SetHackTeams(on bool) { h.hackTeams = on }

// SetSignInAlerts shares the sign-in alert sender (UserHandler.SignInAlerts).
func (h *ConnectorHandler) SetSignInAlerts(a *SignInAlerts) { h.alerts = a }

// connectedAppName is how a sign-in alert names the app: the name it
// registered with (self-declared, so the alert quotes it).
func connectedAppName(name string) string { return httpx.CleanClientName(name) }

// ResolveConsentUserForTest is resolveConsentUser, exported as a test hook
// (cleanup 3.2 slice 3): the moved hack_m2_test.go
// (TestHackM2ConnectorBoundToTeam) checks that a team key fails consent
// resolution.
func (h *ConnectorHandler) ResolveConsentUserForTest(ctx context.Context, key string) (model.User, int, string) {
	return h.resolveConsentUser(ctx, key)
}

// resolveConsentUser authenticates the person deciding, by the API key the
// sign-in on this page (or the dashboard) left in the browser.
func (h *ConnectorHandler) resolveConsentUser(ctx context.Context, key string) (model.User, int, string) {
	if key == "" {
		return model.User{}, http.StatusUnauthorized, "sign in first"
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return model.User{}, http.StatusForbidden, "The instance admin key cannot connect apps. Sign in with a personal account."
	}
	user, err := h.accountsSvc().GetUserByAPIKey(ctx, key, h.limits.KeyIdleExpiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, http.StatusUnauthorized, "sign in again"
		}
		if errors.Is(err, model.ErrAccountSuspended) {
			return model.User{}, http.StatusForbidden, "This account has been suspended by the operator, so it cannot connect apps."
		}
		return model.User{}, http.StatusInternalServerError, "internal server error"
	}
	// The built-in admin-key account is not a person and never connects an
	// app. A person who happens to be an admin connects like anyone else
	// (owner decision 2026-09-24).
	if user.Username == "admin" {
		return model.User{}, http.StatusForbidden, "This account cannot connect apps. Sign in with a personal account."
	}
	// A team key acts as the event, not as a person: it never connects an
	// app (hosted events).
	if user.Team != nil {
		return model.User{}, http.StatusForbidden, "A team key cannot connect apps. Sign in with your own account."
	}
	return user, 0, ""
}

// decide records Allow or Cancel from the consent screen and returns where to
// send the browser. It is the only place an authorization code is minted.
func (h *ConnectorHandler) decide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Same-origin only. The API key header already rules out a cross-site
	// form, and this rules out a cross-site script as a second fence.
	if o := r.Header.Get("Origin"); o != "" && !strings.EqualFold(strings.TrimRight(o, "/"), h.issuer) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin request refused"})
		return
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-site request refused"})
		return
	}
	var body struct {
		Query    string `json:"query"`
		CSRF     string `json:"csrf"`
		Decision string `json:"decision"`
		TeamID   string `json:"team_id"`
		Mode     string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	q, err := url.ParseQuery(strings.TrimPrefix(body.Query, "?"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
		return
	}
	q.Del("token") // sign-in plumbing of the consent page, not part of the request
	q.Del("cn")
	req, aerr := h.parseAuthorize(r.Context(), q)
	if aerr != nil && !aerr.redirect {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: aerr.description})
		return
	}
	if !h.checkConsentCSRF(req, body.CSRF) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "This page has expired. Go back to the app and connect again."})
		return
	}
	if aerr != nil {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.errorRedirect(req, aerr)})
		return
	}
	switch body.Decision {
	case "deny":
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.errorRedirect(req, &authzError{code: "access_denied", description: "The person cancelled."})})
		return
	case "allow":
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "decision must be allow or deny"})
		return
	}
	user, status, msg := h.resolveConsentUser(r.Context(), r.Header.Get("X-API-Key"))
	if status != 0 {
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	// The knowledge server is the owner's alone; no one else gets a code
	// for it (the endpoint checks again on every request).
	if h.brainResource != "" && req.Resource == h.brainResource && !brainOwner(user) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: brainOwnerOnly, Code: "owner_only"})
		return
	}
	scope := auth.OAuthScope
	if h.hackTeams {
		// A consent page left open across deployment may still submit the old
		// team-only choice. Do not silently turn that narrower approval into a
		// personal grant; ask the person to reload the current consent page.
		if body.Mode != "" || body.TeamID != "" {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "The connection options changed. Reload this page and allow again.", Code: "consent_updated"})
			return
		}
		scope = auth.OAuthScope + " " + auth.HackEventsScope
	}
	code := randomToken(prefixCode, 32)
	if err := h.accountsSvc().InsertOAuthCode(r.Context(), hashSecret(code), model.OAuthCode{
		ClientID:      req.Client.ClientID,
		UserID:        user.ID,
		RedirectURI:   req.RedirectURI,
		CodeChallenge: req.CodeChallenge,
		Scope:         scope,
		Resource:      req.Resource,
		ExpiresAt:     h.now().Add(oauthCodeTTL),
		Device:        connectionDevice(r.UserAgent()),
	}); err != nil {
		log.Printf("connector: insert code: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("connector: consent user_id=%s client_id=%s", user.ID, req.Client.ClientID)
	h.alerts.Alert(user.ID, r.UserAgent(), connectedAppName(req.Client.Name))
	writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.redirectWith(req.RedirectURI, map[string]string{"code": code, "state": req.State})})
}

// ---- token endpoint ----------------------------------------------------------------

// authenticateClient applies RFC 6749 §2.3 to a token or revocation request.
// A confidential client must prove its secret (Basic or in the body); a
// public client identifies itself by client_id alone and is held to PKCE.
func (h *ConnectorHandler) authenticateClient(r *http.Request) (model.OAuthClient, bool, string) {
	formID, formSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	basicID, basicSecret, hasBasic := r.BasicAuth()
	if hasBasic {
		// Basic credentials are form-encoded before base64 (RFC 6749 §2.3.1).
		if v, err := url.QueryUnescape(basicID); err == nil {
			basicID = v
		}
		if v, err := url.QueryUnescape(basicSecret); err == nil {
			basicSecret = v
		}
		if formSecret != "" || (formID != "" && formID != basicID) {
			return model.OAuthClient{}, false, "use one client authentication method"
		}
		formID, formSecret = basicID, basicSecret
	}
	if formID == "" {
		return model.OAuthClient{}, false, "client_id is required"
	}
	client, err := h.accountsSvc().GetOAuthClient(r.Context(), formID)
	if err != nil {
		// Hash anyway so an unknown client costs what a known one does.
		_ = hashSecret(formSecret)
		return model.OAuthClient{}, false, "unknown client"
	}
	if client.SecretHash.Valid {
		if formSecret == "" || subtle.ConstantTimeCompare([]byte(hashSecret(formSecret)), []byte(client.SecretHash.String)) != 1 {
			return model.OAuthClient{}, false, "client authentication failed"
		}
	}
	return client, true, ""
}

func (h *ConnectorHandler) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			oauthError(w, http.StatusBadRequest, "invalid_request", "parameter "+k+" is repeated")
			return
		}
	}
	client, ok, why := h.authenticateClient(r)
	if !ok {
		if _, _, basic := r.BasicAuth(); basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="simple-host"`)
		}
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	_ = h.accountsSvc().TouchOAuthClient(r.Context(), client.ClientID)
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		h.redeemCode(w, r, client)
	case "refresh_token":
		h.refresh(w, r, client)
	case "":
		oauthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (h *ConnectorHandler) redeemCode(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	reply, err := h.connectorSvc().RedeemCode(r.Context(), accounts.ConnectorTokenInput{Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"), Verifier: r.PostForm.Get("code_verifier"), Resource: r.PostForm.Get("resource"), RefreshToken: r.PostForm.Get("refresh_token"), Scope: r.PostForm.Get("scope")}, client)
	h.writeConnectorTokens(w, reply, err)
}

func (h *ConnectorHandler) refresh(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	reply, err := h.connectorSvc().Refresh(r.Context(), accounts.ConnectorTokenInput{Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"), Verifier: r.PostForm.Get("code_verifier"), Resource: r.PostForm.Get("resource"), RefreshToken: r.PostForm.Get("refresh_token"), Scope: r.PostForm.Get("scope")}, client)
	h.writeConnectorTokens(w, reply, err)
}

// revoke is RFC 7009. It answers 200 whether or not the token was known, so it
// cannot be used to test tokens.
func (h *ConnectorHandler) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	client, ok, why := h.authenticateClient(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	token := r.PostForm.Get("token")
	w.Header().Set("Cache-Control", "no-store")
	if token == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	tokenHash := hashSecret(token)
	if tok, err := h.accountsSvc().GetOAuthToken(r.Context(), tokenHash); err == nil && tok.ClientID == client.ClientID {
		if tok.Kind == "refresh" {
			err = h.accountsSvc().DeleteOAuthGrant(r.Context(), tok.GrantID)
		} else {
			err = h.accountsSvc().DeleteOAuthToken(r.Context(), tokenHash)
		}
		if err != nil {
			log.Printf("connector: revoke: %v", err)
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "")
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ---- bearer tokens as identity -------------------------------------------------------

// userForAccessToken resolves an access token to its person. audiences lists
// the grant resources acceptable where it is being presented.
func (h *ConnectorHandler) userForAccessToken(ctx context.Context, token string, audiences ...string) (model.User, bool) {
	u, _, ok := h.userForAccessTokenTeam(ctx, token, audiences...)
	return u, ok
}

// userForAccessTokenTeam is userForAccessToken plus the team site the
// connection is bound to ("" for none).
func (h *ConnectorHandler) userForAccessTokenTeam(ctx context.Context, token string, audiences ...string) (model.User, string, bool) {
	u, teamID, _, _, ok := h.tokenAccess(ctx, token, audiences...)
	return u, teamID, ok
}

// tokenAccess resolves an access token. mode is mcp.CallerModeEvents for an
// personal hosted grant and "" otherwise. A malformed hosted grant cannot
// authenticate. The returned grant also carries the selected team, if any.
func (h *ConnectorHandler) tokenAccess(ctx context.Context, token string, audiences ...string) (model.User, string, string, model.OAuthTokenGrant, bool) {
	return h.accountsSvc().ConnectorAccess(ctx, token, h.now, h.hackTeams, audiences...)
}

// selectHackTeam is called only by the hosted MCP team-selection tool. A
// grant cannot select a team unless its person is a current participant on it.
func (h *ConnectorHandler) selectHackTeam(ctx context.Context, caller mcp.Caller, teamID string) error {
	return appsites.New(h.database, nil, nil, 0, appsites.Deps{}).SelectConnectionTeam(ctx, h.hackTeams, caller.GrantID, caller.UserID, teamID)
}

// internalKeyFor mints the per-request credential for a connection: bound to
// its team's site when teamID is set, otherwise the person's own account.
// On the hackathon platform the person's own account is issued only for an
// explicit events grant. That account still cannot publish a personal site.
func (h *ConnectorHandler) internalKeyFor(userID, teamID string) (string, func(), error) {
	if teamID != "" {
		return h.accountsSvc().InternalKeyFor(userID, teamID)
	}
	return h.accountsSvc().InternalKeyFor(userID, "")
}

func bearerToken(r *http.Request) (string, bool) {
	authz := r.Header.Get("Authorization")
	if len(authz) < 7 || !strings.EqualFold(authz[:7], "bearer ") {
		return "", false
	}
	return strings.TrimSpace(authz[7:]), true
}

// BearerAuth lets a connector access token stand in for the person's API key
// on the REST API. The token is exchanged in process for an internal
// credential that resolves to the same person (their key itself is stored
// only as a hash), so the request then meets exactly the checks, limits and
// permissions a request with their key meets. A bearer value that is not one of this server's
// tokens is left alone (the key middleware then explains X-API-Key).
func (h *ConnectorHandler) BearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") || r.Header.Get("X-API-Key") != "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerToken(r)
		if !ok || !strings.HasPrefix(token, prefixAccess) {
			next.ServeHTTP(w, r)
			return
		}
		user, teamID, ok := h.userForAccessTokenTeam(r.Context(), token, h.issuer)
		if !ok && user.Suspended {
			writeAccountSuspended(w)
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+h.issuer+`/.well-known/oauth-protected-resource"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "the access token is invalid, expired or revoked; refresh it or reconnect",
				"code":  "invalid_token",
			})
			return
		}
		key, revoke, err := h.internalKeyFor(user.ID, teamID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		defer revoke()
		// Marked so a route that must refuse a connected app (changing the
		// account's own password) can tell this in-process key apart from
		// one the person actually typed in (isConnectorRequest).
		r2 := r.Clone(context.WithValue(r.Context(), connectorTokenKey{}, true))
		r2.Header.Del("Authorization")
		r2.Header.Set("X-API-Key", key)
		next.ServeHTTP(w, r2)
	})
}

// connectorTokenKey marks a request whose X-API-Key was minted in process
// from a connector's OAuth access token (BearerAuth above), not sent by the
// person. isConnectorRequest reads it; checked by routes a connected app
// must never reach even though it otherwise carries the person's full REST
// permissions, such as changing the account's own password.
type connectorTokenKey struct{}

func isConnectorRequest(ctx context.Context) bool {
	v, _ := ctx.Value(connectorTokenKey{}).(bool)
	return v
}

// ---- /mcp -------------------------------------------------------------------------------

func (h *ConnectorHandler) mcpUnauthorized(w http.ResponseWriter, presented bool) {
	code := "sign_in_required"
	challenge := `Bearer resource_metadata="` + h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + auth.OAuthScope + `"`
	if presented {
		code = "invalid_token"
		challenge = `Bearer error="invalid_token", error_description="The access token is invalid, expired or revoked", resource_metadata="` +
			h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + auth.OAuthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "sign in to Simple Host to use this connector", Code: code})
}

func (h *ConnectorHandler) serveMCP(w http.ResponseWriter, r *http.Request) {
	var apiKey, teamAPIKey, mode, grantID, userID, owner, oauthGrantID, appName, accountHandle string
	var deployOnly bool
	var credentialExpiresAt time.Time
	var mintUpload func(context.Context, bool) (string, func(), error)
	var mintPhoto func(context.Context, bool) (string, func(), error)
	if token, ok := bearerToken(r); ok {
		user, teamID, grantMode, grant, valid := h.tokenAccess(r.Context(), token, h.mcpResource, h.issuer)
		if !valid && user.Suspended {
			writeAccountSuspended(w)
			return
		}
		if !valid {
			h.mcpUnauthorized(w, true)
			return
		}
		key, revoke, err := h.internalKeyFor(user.ID, teamID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		defer revoke()
		// The minted key stands for the connector, not the person: mark the
		// request so REST calls bridged from MCP are refused where a
		// connected app must never act (isConnectorRequest).
		r = r.WithContext(context.WithValue(r.Context(), connectorTokenKey{}, true))
		apiKey, mode, owner = key, grantMode, user.ID
		accountHandle = user.Handle.String
		oauthGrantID = grant.GrantID
		credentialExpiresAt = grant.ExpiresAt
		client, err := h.accountsSvc().GetOAuthClient(r.Context(), grant.ClientID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		appName = httpx.CleanClientName(client.Name)
		mintPhoto = func(ctx context.Context, _ bool) (string, func(), error) {
			return h.accountsSvc().PhotoCredential(ctx, grant.GrantID, user.ID, grant.ClientID, h.mcpResource)
		}
		mintUpload = func(ctx context.Context, team bool) (string, func(), error) {
			u, currentTeam, _, currentGrant, valid := h.tokenAccess(ctx, token, h.mcpResource, h.issuer)
			if !valid {
				return "", nil, errors.New("upload sign-in ended")
			}
			if team {
				if !currentGrant.SelectedTeamID.Valid {
					return "", nil, errors.New("selected team no longer resolves")
				}
				currentTeam = currentGrant.SelectedTeamID.String
				if _, err := h.accountsSvc().ResolveTeamIdentity(ctx, u.ID, currentTeam); err != nil {
					return "", nil, err
				}
			}
			return h.internalKeyFor(u.ID, currentTeam)
		}
		if h.hackTeams && grantMode == mcp.CallerModeEvents {
			grantID, userID = grant.GrantID, user.ID
			if grant.SelectedTeamID.Valid {
				if _, err := h.accountsSvc().ResolveTeamIdentity(r.Context(), user.ID, grant.SelectedTeamID.String); err == nil {
					teamAPIKey, revoke, err = h.internalKeyFor(user.ID, grant.SelectedTeamID.String)
					if err != nil {
						writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
						return
					}
					defer revoke()
				} else if !errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
					return
				}
			}
		}
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		// Coding agents that already hold a key can use the endpoint too.
		// The key's own scope is unchanged. On the hackathon platform a
		// personal key is the same identity as an events grant, so it sees
		// the event tools; a team key keeps the website tools.
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
			if u, err := h.accountsSvc().GetUserByAPIKey(r.Context(), key, h.limits.KeyIdleExpiry); err != nil {
				if errors.Is(err, model.ErrAccountSuspended) {
					writeAccountSuspended(w)
					return
				}
				if errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle) {
					// Say why, with no OAuth challenge: the caller holds a key
					// and needs a new one, not a sign-in flow.
					msg, code := auth.ExpiredKeyMessage(err, u.KeyExpiresAt, h.limits.KeyIdleExpiry)
					w.Header().Set("Cache-Control", "no-store")
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msg, Code: code})
					return
				}
				h.mcpUnauthorized(w, true)
				return
			} else {
				owner = u.ID
				accountHandle = u.Handle.String
				if u.KeyExpiresAt != nil {
					credentialExpiresAt = *u.KeyExpiresAt
				}
				deployOnly = u.KeyScope == "deploy"
				if h.hackTeams && u.Team == nil && u.KeyScope != "deploy" {
					mode = mcp.CallerModeEvents
				}
			}
		}
		apiKey = key
		mintUpload = func(ctx context.Context, _ bool) (string, func(), error) {
			if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
				if _, err := h.accountsSvc().GetUserByAPIKey(ctx, key, h.limits.KeyIdleExpiry); err != nil {
					return "", nil, err
				}
			}
			return key, func() {}, nil
		}
	} else {
		h.mcpUnauthorized(w, false)
		return
	}
	if h.hackTeams && mode != mcp.CallerModeEvents {
		mode = mcp.CallerModeHackSite
	}
	h.mcp.ServeHTTP(w, r.WithContext(mcp.WithCaller(r.Context(), mcp.Caller{
		APIKey: apiKey, TeamAPIKey: teamAPIKey, Mode: mode, GrantID: grantID, UserID: userID, Owner: owner, MintUpload: mintUpload,
		OAuthGrantID: oauthGrantID, AppName: appName, DeployOnly: deployOnly,
		CredentialExpiresAt: credentialExpiresAt, MintPhoto: mintPhoto, Handle: accountHandle,
	})))
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
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	teams := map[string][]model.OAuthConnectionTeam{}
	if h.ed.Kind() == edition.Hack {
		teams, err = h.accountsSvc().OAuthConnectionTeams(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
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
		if h.ed.Kind() == edition.Hack {
			selected := teams[c.ClientID]
			if selected == nil {
				selected = []model.OAuthConnectionTeam{}
			}
			item["teams"] = selected
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
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
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
	return accounts.NewConnector(h.database, h.now, h.normalizeResource, h.hackTeams, h.oauthAccessTTL(), h.oauthRefreshTTL())
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
