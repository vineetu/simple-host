package accesshttp

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
	"database/sql"
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

	authrules "github.com/vsriram/simple-host/host/access/rules"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

const (
	oauthCodeTTL         = 60 * time.Second
	oauthConsentTTL      = 30 * time.Minute
	oauthMaxRedirectURIs = 10
)

// ConnectorHandler serves the OAuth authorization server and /mcp.
type ConnectorHandler struct {
	sweepWorkerOnce sync.Once
	sweepWorker     *connectorSweepWorker
	httpCaches      httpx.Caches
	ed              *edition.Edition
	Limits          edition.Limits
	hostedEvents    bool
	database        *sql.DB
	Issuer          string // e.g. https://simple-host.app
	mcpResource     string // issuer + "/mcp"
	adminAPIKey     string
	mcp             *mcp.Server
	MCPConfig       mcp.Config
	upstream        http.Handler
	skillVersion    string

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
	UploadLimiter    *httpx.RateLimiter

	now func() time.Time
	// teamConnections is on when the product has team connections: a new
	// grant is personal and selects a team (SetTeamConnections).
	teamConnections bool

	// assets is the asset/chrome pipeline for the consent screen. Read it through pipeline().
	assets     *web.AssetPipeline
	assetsOnce sync.Once
}

// pipeline is h's asset pipeline. A bare &ConnectorHandler{} (tests) gets one
// for its (possibly nil, so Host) edition on first use.
func (h *ConnectorHandler) pipeline() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = web.NewAssetPipeline(h.ed, h.Issuer)
		}
	})
	return h.assets
}

// NewConnectorHandlerForTest builds a minimal *ConnectorHandler for
// discovery-only tests: the issuer and edition policy, with calls
// served through mcpServer directly rather than building its own
// internally, so a test can compare its discovery documents against that
// exact *mcp.Server instance.
func NewConnectorHandlerForTest(issuer string, mcpServer *mcp.Server, ed *edition.Edition) *ConnectorHandler {
	return &ConnectorHandler{Issuer: issuer, mcp: mcpServer, ed: ed, hostedEvents: ed.Routes().HostedEvents}
}

// MCPUpstreamForTest exposes the exact handler configured for MCP tool calls so
// product wiring tests can exercise it with visitor-session credentials.
func (h *ConnectorHandler) MCPUpstreamForTest() http.Handler {
	return h.MCPConfig.Upstream
}

// NewConnectorHandler builds the connector. upstream is the application mux
// behind the key scope gate (auth.ScopeGate): MCP tool calls are served into
// it in process, so a deploy-only key's tools meet the same route table.
// access gives tools the visitor reference key notifications use
// (AccessHandler.VisitorRefKey); alerts emails the owner when an app is
// connected (UserHandler.SignInAlerts). Either may be nil.
func NewConnectorHandler(database *sql.DB, publicBaseURL, adminAPIKey, siteDomain, contentHost, skillVersion string, upstream http.Handler, hostedEvents bool, access *AccessHandler, alerts *SignInAlerts, editions ...*edition.Edition) *ConnectorHandler {
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
		Limits:       lim,
		database:     database,
		Issuer:       issuer,
		mcpResource:  issuer + "/mcp",
		adminAPIKey:  adminAPIKey,
		consentKey:   key,
		alerts:       alerts,
		// Registration: a chat app registers once per install, so a handful
		// an hour per address is plenty. Authorize and token: one person's
		// sign-in or a client's refresh loop, with room for retries.
		registerLimiter:  httpx.NewRateLimiterFor(lim.RateOAuthRegister.Burst, lim.RateOAuthRegister.Every),
		authorizeLimiter: httpx.NewRateLimiterFor(lim.RateOAuthAuthorize.Burst, lim.RateOAuthAuthorize.Every),
		tokenLimiter:     httpx.NewRateLimiterFor(lim.RateOAuthToken.Burst, lim.RateOAuthToken.Every),
		UploadLimiter:    httpx.NewRateLimiterFor(lim.RateOAuthToken.Burst, lim.RateOAuthToken.Every),
		now:              time.Now,
	}
	h.httpCaches.Rate("http.connector.registerLimiter", h.registerLimiter, 10*time.Minute, 2*time.Hour)
	h.httpCaches.Rate("http.connector.authorizeLimiter", h.authorizeLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.connector.tokenLimiter", h.tokenLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.connector.uploadLimiter", h.UploadLimiter, 10*time.Minute, 30*time.Minute)
	h.upstream = upstream
	h.skillVersion = skillVersion
	mcpSnapshot := mcp.LimitsOf(lim)
	h.MCPConfig = mcp.Config{
		Limits:             &mcpSnapshot,
		AnalyticsProjectID: strings.TrimSpace(os.Getenv("AGENTCAT_PROJECT_ID")),
		AnalyticsEnv:       analyticsEnv(issuer, ed.Presentation().CanonicalBaseURL),
		AnalyticsProduct:   analyticsProduct(ed.Presentation().ProductName),
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
		MaxBodyBytes: sites.JSONBodyLimit(sites.LargestSiteLimit(h.Limits)),
		// host/mcp is a leaf package (check-layering.sh) and may not
		// import host/papermetrics directly.
		MutationResult: func(ctx context.Context, method, path string, status int) {
			papermetrics.From(ctx).MutationResult(ctx, method, path, status)
		},
		// main sets it from the server's Turnstile keys (OfferSpamCheck).
		SelfHosted: siteDomain != web.CanonicalSiteDomain,
		// Teams are offered where accounts own their own sites.
		Teams: ed.Identity().PersonalSites,
	}
	// Tools hand out the same opaque visitor reference notifications carry
	// (derived from PASSCODE_ENC_KEY, else the process-local export key).
	if access != nil {
		h.MCPConfig.VisitorRefs = accounts.NewVisitorReferences(access.VisitorRefKey())
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
	// simple-hack.app (hostedEvents=true, siteDomain != CanonicalSiteDomain
	// since that constant is Host-specific) keeps its own text unchanged.
	if web.InstanceHosts != nil && "https://"+siteDomain != h.ed.Presentation().CanonicalBaseURL {
		h.MCPConfig.Rewrite = web.InstanceHosts.R.Replace
	}
	h.mcp = mcp.NewServer(h.MCPConfig)
	return h
}

// SetPreviewBases follows the same served bases as the site's URL routing.
// Call once at startup, before serving.
func (h *ConnectorHandler) SetPreviewBases(bases []string) {
	h.MCPConfig.SiteBases = append([]string(nil), bases...)
	h.mcp = mcp.NewServer(h.MCPConfig)
}

// OfferSpamCheck tells the connector whether this server runs the spam check
// (SiteHandler.SpamCheckEnabled), so its tools offer auth.checked
// only where the server takes it. Call once at startup, before serving.
func (h *ConnectorHandler) OfferSpamCheck(on bool) {
	h.MCPConfig.NoSpamCheck = !on
	h.mcp = mcp.NewServer(h.MCPConfig)
}

// SetTools installs a product's AI tools. Call once at startup, before
// serving; the routes Register added read the server at request time.
func (h *ConnectorHandler) SetTools(product mcp.Contribution) {
	h.MCPConfig.Contribution = &product
	h.mcp = mcp.NewServer(h.MCPConfig)
}

// SetEventsBackend enables MCP Events. Call once at startup, before serving.
func (h *ConnectorHandler) SetEventsBackend(b mcp.EventsBackend) {
	h.MCPConfig.Events = b
	h.mcp = mcp.NewServer(h.MCPConfig)
}

// connectorSweepWorker removes expired codes and tokens, abandoned registrations, and
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
					n, err := h.accountsSvc().SweepOAuth(ctx, h.Limits.OAuthUnusedClientAge)
					if err != nil {
						w.tracker.Fail("sweep OAuth", err)
					}
					if n > 0 {
						w.tracker.Progress()
					}
					if n, err := h.accountsSvc().PurgeExpiredAPIKeys(ctx, h.Limits.KeyIdleExpiry); err != nil {
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

func (h *ConnectorHandler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	web.RegisterAgentCard(mux, h.Issuer, func() *mcp.Server { return h.mcp }, h.hostedEvents)
	web.RegisterMCPServerCard(mux, h.Issuer, h.hostedEvents, h.ed.Presentation())
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.authorizationServerMetadata)
	// Some clients derive the metadata address by inserting the MCP path.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", h.authorizationServerMetadata)
	mux.HandleFunc("GET "+MuseClientPath, h.museClientMetadata)

	mux.Handle("POST /oauth/register", httpx.RateLimitByIP(h.registerLimiter, http.HandlerFunc(h.register)))
	mux.Handle("GET /oauth/authorize", httpx.RateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.authorize)))
	mux.Handle("POST /oauth/authorize/decision", httpx.RateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.decide)))
	// Answers 404 unless the reviewer account is configured.
	// The one other route that answers with an API key: the app's own address only.
	mux.Handle("POST /oauth/reviewer-signin", web.AccountAuthOnly(h.Issuer, http.HandlerFunc(h.reviewerSignInHandler)))
	mux.Handle("POST /oauth/token", httpx.RateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.token)))
	mux.Handle("POST /oauth/revoke", httpx.RateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.revoke)))

	// POST carries every message. GET and DELETE are answered 405 by the MCP
	// server (no standalone stream, no sessions), after authentication, so an
	// unauthenticated probe of any method learns where to sign in.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		mux.HandleFunc(m+" /mcp", h.serveMCP)
	}

	photoLinks := web.PhotosHostOnly(!h.hostedEvents, web.AccountAuthOnly(h.Issuer, httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServePhotoLink(w, r) }))))
	mux.Handle("GET /v1/photo-links", photoLinks)
	mux.Handle("POST /v1/photo-links", photoLinks)
	mux.Handle("PUT /v1/uploads/{token}", httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServeUpload(w, r) })))
	mux.Handle("POST /v1/uploads/{token}", httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.mcp.ServeUpload(w, r) })))

	mux.Handle("GET /v1/me/connections", authMiddleware(http.HandlerFunc(h.listConnections)))
	mux.Handle("DELETE /v1/me/connections/{client_id}", authMiddleware(http.HandlerFunc(h.deleteConnection)))
}

// ---- metadata ---------------------------------------------------------------

func (h *ConnectorHandler) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resource := h.mcpResource
	if r.URL.Path == "/.well-known/oauth-protected-resource" {
		// The root document describes the whole server: tokens for it also
		// work on /v1. MCP clients read the /mcp-suffixed one.
		resource = h.Issuer
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{h.Issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         h.scopesSupported(),
		"resource_name":            "Simple Host",
		"resource_documentation":   h.Issuer + "/llms.txt",
	})
}

func (h *ConnectorHandler) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	authMethods := []string{"none", "client_secret_post", "client_secret_basic"}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         h.Issuer,
		"authorization_endpoint":                         h.Issuer + "/oauth/authorize",
		"token_endpoint":                                 h.Issuer + "/oauth/token",
		"registration_endpoint":                          h.Issuer + "/oauth/register",
		"revocation_endpoint":                            h.Issuer + "/oauth/revoke",
		"scopes_supported":                               h.scopesSupported(),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          authMethods,
		"revocation_endpoint_auth_methods_supported":     authMethods,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          h.Issuer + "/llms.txt",
	})
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	body := map[string]string{"error": code}
	if description != "" {
		body["error_description"] = description
	}
	httpx.WriteJSON(w, status, body)
}

// loopbackPortMatch lets a loopback redirect come back on any port (RFC 8252
// section 7.3): an app on the person's machine picks a free port each time.
// Everything else about the address must match what was registered.
func loopbackPortMatch(registered, presented string) bool {
	reg, err1 := url.Parse(registered)
	pre, err2 := url.Parse(presented)
	if err1 != nil || err2 != nil || reg.Scheme != "http" || pre.Scheme != "http" || !accounts.ValidRedirectURI(presented) {
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

// normalizeResource maps a requested resource to the audience stored on the
// grant: this server's /mcp, or the server as a whole. Anything else is not a
// resource this server serves.
func (h *ConnectorHandler) normalizeResource(raw string) (string, bool) {
	return accounts.NormalizeConnectorResource(h.Issuer, raw)
}

// ---- dynamic client registration (RFC 7591) ------------------------------------

func (h *ConnectorHandler) register(w http.ResponseWriter, r *http.Request) {
	var req accounts.ClientMetadata
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON client metadata document")
		return
	}
	client, secret, err := h.accountsSvc().RegisterClient(r.Context(), req, oauthMaxRedirectURIs)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, h.registeredClient(client, secret))
}

// registeredClient is the registration answer (RFC 7591 §3.2.1).
func (h *ConnectorHandler) registeredClient(client model.OAuthClient, secret string) map[string]any {
	resp := map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        h.now().Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"scope":                      authrules.OAuthScope,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	return resp
}

// CreateOperatorClient registers a confidential client by hand (the
// `simple-host oauth-client create` command): a ChatGPT GPT Action, which is
// configured with a fixed client ID and secret rather than registering itself.
// The secret is returned once and stored only as a hash. pkceRequired=false is
// allowed only here, for platforms that cannot send PKCE.
func CreateOperatorClient(ctx context.Context, database *sql.DB, name string, redirectURIs []string, pkceRequired bool) (string, string, error) {
	for _, u := range redirectURIs {
		if !accounts.ValidRedirectURI(u) {
			return "", "", fmt.Errorf("invalid redirect URI %q: must be https (or http on localhost), with no fragment", u)
		}
	}
	if len(redirectURIs) > oauthMaxRedirectURIs {
		return "", "", fmt.Errorf("at most %d redirect URIs", oauthMaxRedirectURIs)
	}
	secret := accounts.RandomToken(accounts.PrefixSecret, 32)
	client := model.OAuthClient{
		ClientID:                accounts.RandomToken(accounts.PrefixClient, 16),
		SecretHash:              sql.NullString{String: accounts.HashSecret(secret), Valid: true},
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
		if !accounts.ValidRedirectURI(u) {
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
// Client ID Metadata Document at MuseClientPath still works too, since its
// own URL must remain its client_id (that is the point of the document).
// Both are pre-registered (EnsureBuiltinClients), held to PKCE like any
// other public client. Fetching arbitrary client ID URLs is not supported,
// so discovery does not advertise client_id_metadata_document_supported;
// clients that can register keep using /oauth/register.
const (
	MuseClientPath    = "/.well-known/muse-client.json"
	MuseShortClientID = "meta-muse"
	museClientName    = "Meta Muse"
	MuseRedirectURI   = "https://agent.meta.ai/api/hatch/oauth/callback"
)

func (h *ConnectorHandler) museClientID() string { return h.Issuer + MuseClientPath }

// EnsureBuiltinClients creates or refreshes the clients the server defines
// itself. Run at boot; idempotent.
func (h *ConnectorHandler) EnsureBuiltinClients(ctx context.Context) error {
	if err := h.accountsSvc().UpsertBuiltinOAuthClient(ctx, h.museClientID(), museClientName, []string{MuseRedirectURI}); err != nil {
		return err
	}
	return h.accountsSvc().UpsertBuiltinOAuthClient(ctx, MuseShortClientID, museClientName, []string{MuseRedirectURI})
}

// museKeyboardMangled reports a client ID that is one of ours changed on the
// way in: the document URL with something appended (".com" seen live), or
// meta-muse capitalised or padded by autocorrect. Only the help text uses
// this; the exact ids stay the only ones accepted.
func museKeyboardMangled(clientID string) bool {
	lower := strings.ToLower(clientID)
	return strings.Contains(lower, "muse-client") || strings.TrimSpace(lower) == MuseShortClientID
}

func (h *ConnectorHandler) museClientMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"client_id":                  h.museClientID(),
		"client_name":                museClientName,
		"redirect_uris":              []string{MuseRedirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      authrules.OAuthScope,
	})
}

// clientIDShape describes an unknown client ID for the log without
// recording what a person may have typed into it.
func clientIDShape(id string) string {
	switch {
	case strings.HasPrefix(id, accounts.PrefixClient):
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
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	conns, err := h.accountsSvc().ListOAuthConnections(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (h *ConnectorHandler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	n, err := h.accountsSvc().DeleteOAuthGrantsForClient(r.Context(), user.ID, r.PathValue("client_id"))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if n == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "no connected app with that id"})
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

// analyticsEnv tags usage events so test instances can be filtered out:
// "prod" only on the product's own hosted address.
func analyticsEnv(issuer, canonical string) string {
	if issuer == "https://simple-host.app" || (canonical != "" && issuer == canonical) {
		return "prod"
	}
	return "dev"
}

// analyticsProduct tags usage events with the product ("Simple Hack" is
// "simple-hack"), so two products can share one AgentCat project.
func analyticsProduct(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

func (h *ConnectorHandler) accountsSvc() *accounts.Service { return accounts.New(h.database) }
func (h *ConnectorHandler) connectorSvc() *accounts.Connector {
	return accounts.NewConnector(h.database, h.now, h.Issuer, h.teamConnections, h.Limits.OAuthAccessTTL, h.Limits.OAuthRefreshTTL)
}

// writeConnectorError answers an *accounts.ConnectorError with its status.
func writeConnectorError(w http.ResponseWriter, err error) {
	e := err.(*accounts.ConnectorError)
	oauthError(w, connectorErrorStatus(err), e.Code, e.Description)
}

// connectorErrorStatus is the status of a token, revocation or registration
// refusal (an *accounts.ConnectorError): invalid_client 401,
// temporarily_unavailable 503, an internal one 500, the rest 400.
func connectorErrorStatus(err error) int {
	e, ok := err.(*accounts.ConnectorError)
	switch {
	case !ok:
		return 0
	case e.Internal:
		return http.StatusInternalServerError
	case e.Code == "invalid_client":
		return http.StatusUnauthorized
	case e.Code == "temporarily_unavailable":
		return http.StatusServiceUnavailable
	}
	return http.StatusBadRequest
}

func (h *ConnectorHandler) writeConnectorTokens(w http.ResponseWriter, reply accounts.ConnectorTokens, err error) {
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"access_token": reply.AccessToken, "token_type": reply.TokenType, "expires_in": reply.ExpiresIn, "refresh_token": reply.RefreshToken, "scope": reply.Scope})
}

func (h *ConnectorHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
