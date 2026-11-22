package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // Container images have no zoneinfo.

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/config"
	storageapp "github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/geoip"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	operationrepo "github.com/vsriram/simple-host/host/ops/repo"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/analytics"
	"github.com/vsriram/simple-host/host/sites/eventdns"
	"github.com/vsriram/simple-host/host/sites/events"
	"github.com/vsriram/simple-host/host/sites/namepeer"
	"github.com/vsriram/simple-host/host/sites/setup"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

// App is the wired program, before any workers or listeners start.
type App struct {
	Mux       *http.ServeMux
	Handler   http.Handler
	Workers   *workers.Set
	Close     func()
	setupDone <-chan struct{}
}

// Build constructs the program in startup order. redirects maps mux patterns to
// targets for legacy 302 responses. Close releases its dependencies; on failure
// Build releases every dependency it has already opened.
func Build(cfg config.Config, exts []*extension.Extension, redirects map[string]string) (_ *App, err error) {
	if len(exts) > 1 {
		return nil, fmt.Errorf("at most one extension is supported")
	}
	var ext *extension.Extension
	if len(exts) == 1 {
		ext = exts[0]
	}
	if err := loadProductConfig(ext); err != nil {
		return nil, err
	}
	maintenance, err := storage.LockMaintenance(cfg.DataDir, false)
	if err != nil {
		return nil, err
	}
	closeDeps := func() { maintenance.Close() }
	defer func() {
		if err != nil {
			closeDeps()
		}
	}()
	logLimits(cfg, ext)
	db, err := openDatabase(cfg)
	if err != nil {
		return nil, err
	}
	closeDeps = func() { db.Close(); maintenance.Close() }
	diskStorage, err := storage.NewDiskStorage(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("create disk storage: %v", err)
	}
	if err := prepareDatabase(db, cfg, exts); err != nil {
		return nil, err
	}

	// Hosted events (EVENTS=hosted, simple-hack.app): set before any route is
	// registered, because the dashboard, the chrome and the host dispatch read it.
	ed := edition.Default(cfg.Limits)
	if ext != nil && ext.Edition != nil {
		ed = ext.Edition(cfg.Limits)
	}
	hosted := ed.Routes().HostedEvents
	// The product instance's own public address (the asset pipeline's
	// InkBaseURL), captured before setup may rewrite cfg.PublicBaseURL to a
	// saved domain, so construction always sees the configured address.
	var productInstanceURL string
	if hosted {
		productInstanceURL = cfg.PublicBaseURL
		log.Printf("hosted events: on (the hackathon platform at %s)", cfg.PublicBaseURL)
	}
	var early extension.EarlyHooks
	if ext != nil && ext.Early != nil {
		early = ext.Early(extension.EarlyDeps{DB: db, Config: cfg, Edition: ed})
	}
	accountapp.SetGroupHooks(early.Accounts)

	mux := http.NewServeMux()
	setup, err := applySavedSetup(&cfg, db, ed, productInstanceURL)
	if err != nil {
		return nil, err
	}
	if setup != nil {
		setup.Register(mux)
		log.Printf("SETUP MODE: no hostname configured; serving the setup page on %s", net.JoinHostPort(cfg.BindAddr, cfg.Port))
		return &App{Mux: mux, Handler: mux, Workers: &workers.Set{}, Close: closeDeps, setupDone: setup.Done()}, nil
	}

	// Per-site cap: MAX_ARCHIVE_MB or 100 MB. What the disk really holds is
	// reported by /v1/admin/usage.
	log.Printf("per-site limit: %d MB", sites.Upload.Body>>20)
	// Ensure a real admin user row exists so the admin key can own sites.
	adminUserID, err := accountrepo.EnsureAdminUser(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("ensure admin user: %v", err)
	}
	accountsService := accountapp.New(db)
	authMW := auth.Middleware(cfg.AdminAPIKey, adminUserID, accountsService, ed.Limits().KeyIdleExpiry)
	pluginVersion, err := configurePresentation(cfg, ed)
	if err != nil {
		return nil, err
	}
	// Stale-skill notice: only routes wrapped with it compare X-Skill-Version
	// with the bundled plugin version.
	noticeMW := web.NoticeMiddleware(pluginVersion)
	mailer := configureSignIn(cfg, ed, hosted)

	web.RegisterHealthRoutes(mux, db)
	userHandler := accesshttp.NewUserHandler(db, accountsService, mailer, cfg.PublicBaseURL, ed)
	userHandler.SetPasswordSignIn(cfg.PasswordSignIn == "on", cfg.PasswordSelfSignup != "off")
	userHandler.Register(mux, authMW, noticeMW)
	hosts, err := newHosts(cfg, db, diskStorage, ed)
	if err != nil {
		return nil, err
	}
	siteHandler, accessHandler, dataHandler := handler.NewSiteHandlers(ed, productInstanceURL, db, diskStorage, hosts, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.CustomDomainIP, cfg.DeployScript, cfg.AdminAPIKey, cfg.PreviewAccounts, cfg.PreviewTTL, adminUserID, mailer, userHandler.EmailLimiter)
	mountProductAssets(ext, siteHandler, mux)
	userHandler.ShareAssets(siteHandler.Pages())
	userHandler.SetSites(accessHandler)
	if err := configureSites(cfg, db, siteHandler, accessHandler, dataHandler, userHandler); err != nil {
		return nil, err
	}
	handler.Register(mux, siteHandler, accessHandler, dataHandler, authMW, noticeMW)
	web.SetInstanceNote(web.InstanceFacts{
		SiteDomain: cfg.SiteDomain, ContentHost: cfg.ContentHost,
		SharedOrigin: siteHandler.SharedOrigin(),
		SignInEmail:  cfg.EmailSignInEnabled(), SignInProviders: cfg.EnabledVisitorProviders(), OIDCLabel: cfg.OIDCLabel,
		EmailCapable: cfg.ResendAPIKey != "",
		Contact:      auth.SupportContact,
	})
	// Take-down markers on disk follow the database (suspend.go).
	accessHandler.SyncSuspendMarkers(context.Background())
	oauthHandler := accesshttp.NewOAuthHandler(db, accountsService, OAuthSettings(cfg, accountsService), accessHandler, ed)
	oauthHandler.ShareAssets(siteHandler.Pages())
	accessHandler.SetSSOHop(oauthHandler)
	oauthHandler.Register(mux)

	// Deploy-only keys are refused everywhere but the deploy routes, in this one
	// gate (host/access/auth/scope.go). The connector's MCP tools are served
	// into the gated mux, as the person, so they meet the same checks as the
	// REST call they stand for. The product's write gate is set once the
	// extension is wired below.
	var writeGate sites.GroupGate
	scoped := auth.ScopeGate(accountsService, mux, ed.Limits().KeyIdleExpiry, func(ctx context.Context, user *model.User, site string) (int, string, string, error) {
		return sites.CheckWrite(ctx, db, writeGate, user, site)
	})
	gated := web.APIErrors(mux, scoped, cfg.PublicBaseURL)
	// The connector's text names people's addresses under the base in use.
	mcp.SetAddressBase(cfg.SiteDomain, siteHandler.HandoutBase())
	connector := accesshttp.NewConnectorHandler(db, cfg.PublicBaseURL, cfg.AdminAPIKey, cfg.SiteDomain, cfg.ContentHost, pluginVersion, gated, hosted, accessHandler, userHandler.SignInAlerts(), ed)
	connector.ShareAssets(siteHandler.Pages())
	handler.SetVisitorTools(connector, accessHandler, dataHandler, oauthHandler)
	connector.SetPreviewBases(siteHandler.ServedBases())
	connector.OfferSpamCheck(dataHandler.SpamCheckEnabled())
	connector.SetEventsBackend(handler.EventsBackend(dataHandler))
	connector.Register(mux, authMW)
	if err := connector.EnsureBuiltinClients(context.Background()); err != nil {
		log.Printf("connector: built-in clients: %v", err)
	}
	connector.EnableReviewerSignIn(cfg.ReviewAccountEmail, cfg.ReviewAccountPasswordHash)
	userHandler.SetReviewerEmail(cfg.ReviewAccountEmail)
	userHandler.SetPreviewAccounts(cfg.PreviewAccounts)
	dataHandler.SetWebPushAccounts(cfg.WebPushAccounts, cfg.WebPushAccountsAll)
	web.RegisterOpenAIAppsChallenge(mux, cfg.OpenAIAppsChallenge)
	siteshttp.RegisterUIRoutes(mux, cfg.PublicBaseURL, siteHandler)
	for pattern, target := range redirects {
		mux.Handle(pattern, http.RedirectHandler(target, http.StatusFound))
	}
	siteshttp.RegisterSkillsHub(mux, cfg.PublicBaseURL, siteHandler)

	parts, err := wireExtension(ext, extension.Deps{
		DB: db, Disk: diskStorage, Mail: mailer, Accounts: accountsService,
		Site: siteHandler, Data: dataHandler, Connector: connector,
		AuthMW: authMW, Mux: mux, Config: cfg, Edition: ed,
	})
	if err != nil {
		return nil, err
	}
	connector.SetTools(parts.Tools)
	siteHandler.SetSitemapFilter(parts.Hooks.Sitemap)
	hosts.SetGroupHosts(parts.Hooks.GroupHosts)
	siteHandler.SetGroupGate(parts.Hooks.GroupGate)
	writeGate = parts.Hooks.GroupGate
	handler.SetStorageBindings(accessHandler, dataHandler, parts.Hooks.Storage)
	eventDomains := registerEventDomains(cfg, db, ed, mux, authMW)

	// Caller location comes from local DB-IP files only (no network lookup);
	// the watcher picks up the monthly refresh without a restart.
	geo := geoip.Open(cfg.GeoIPDir)
	closeDeps = func() { geo.Close(); db.Close(); maintenance.Close() }
	// SIGNUP_BLOCKED_COUNTRIES refuses every email-code and OAuth sign-in or
	// sign-up from a listed country; API keys keep working (signupgeo.go).
	signupGeoBlock := accountapp.NewSignupGeoBlock(geo, cfg.SignupBlockedCountries)
	userHandler.SetSignupGeoBlock(signupGeoBlock)
	accessHandler.SetSignupGeoBlock(signupGeoBlock)
	oauthHandler.SetSignupGeoBlock(signupGeoBlock)
	if len(cfg.SignupBlockedCountries) > 0 {
		log.Printf("signup geo-block: sign-in and sign-up refused from %s; API keys and site viewing unaffected", strings.Join(cfg.SignupBlockedCountries, ", "))
	}
	// Per-endpoint API analytics: every /v1/* call that matches a route is
	// counted into daily aggregates; calls from this box are counted apart.
	apiMetrics := web.NewAPIMetrics(operations.NewAPIMetrics(db, geo, ed))
	apiMetrics.SetRouting(mux, append([]string{cfg.CustomDomainIP}, cfg.CustomDomainIPAlso...)...)
	mux.Handle("GET /v1/admin/api-analytics", authMW(http.HandlerFunc(apiMetrics.AdminSummary)))
	mux.Handle("GET /v1/admin/growth", authMW(http.HandlerFunc(apiMetrics.AdminGrowth)))
	paper := paperMetrics(cfg, db, hosted, dataHandler, parts, mux, authMW)
	ingester := analyticsIngester(cfg, db, siteHandler, accessHandler)
	siteHandler.SetTrafficLog(cfg.AnalyticsLog != "")
	siteHandler.SetNetworkUsage(cfg.NetworkInterface)

	app := web.SecurityHeaders(web.CORS(apiMetrics.Wrap(connector.BearerAuth(gated))))
	served := publicHTTP(siteHandler, app, paper, parts)
	workerSet := registerWorkers(siteHandler, dataHandler, userHandler, oauthHandler, connector, parts, apiMetrics.APIMetrics, geo, eventDomains, ingester, paper)
	siteHandler.SetWorkerStatus(workerSet.Status)
	return &App{Mux: mux, Handler: served, Workers: workerSet, Close: closeDeps}, nil
}

// mountProductAssets hands the product's files and asset routes to Sites.
func mountProductAssets(ext *extension.Extension, siteHandler *siteshttp.SiteHandler, mux *http.ServeMux) {
	if ext == nil || ext.Assets == nil {
		return
	}
	assets := ext.Assets(siteHandler)
	siteHandler.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
	if assets.Routes != nil {
		assets.Routes(mux, siteHandler.AssetServices())
	}
}

// wireExtension asks the product for its parts; none without a product.
func wireExtension(ext *extension.Extension, deps extension.Deps) (extension.Parts, error) {
	if ext == nil || ext.Wire == nil {
		return extension.Parts{}, nil
	}
	parts, err := ext.Wire(deps)
	if err != nil {
		return parts, fmt.Errorf("wire extension %s: %w", ext.Name, err)
	}
	return parts, nil
}

func loadProductConfig(ext *extension.Extension) error {
	if ext == nil || ext.Config.Load == nil {
		return nil
	}
	warnings, err := ext.Config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load product config: %w", err)
	}
	for _, warning := range warnings {
		log.Printf("WARNING: %s", warning)
	}
	return nil
}

func logLimits(cfg config.Config, ext *extension.Extension) {
	if changed := cfg.Limits.Changed(); len(changed) > 0 {
		log.Printf("limits changed from the defaults: %s", strings.Join(changed, " "))
	}
	var productSettings []config.Setting
	if ext != nil {
		productSettings = ext.Config.Settings
	}
	for _, w := range cfg.Limits.Warnings(os.Environ(), productSettings...) {
		log.Printf("WARNING: %s", w)
	}
}

func openDatabase(cfg config.Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)
	return db, nil
}

// prepareDatabase checks the product identity before any database write, then
// migrates (MIGRATE_ON_START), verifies the schema and ensures the install
// identity. Migration takes the same lock again and rechecks, so a concurrent
// first start cannot pass the check and then adopt the other program's schema.
func prepareDatabase(db *sql.DB, cfg config.Config, exts []*extension.Extension) error {
	ctx := context.Background()
	if cfg.MigrateOnStart {
		if err := waitForDB(db, 2*time.Minute); err != nil {
			return err
		}
	}
	if err := migrations.WithLock(ctx, db, func(conn *sql.Conn) error {
		return checkProduct(ctx, conn, exts, false)
	}); err != nil {
		return fmt.Errorf("product check: %w", err)
	}
	if cfg.MigrateOnStart {
		log.Printf("MIGRATE_ON_START: building the schema if the database is empty, then applying pending migrations")
		migrateCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		err := migrateDatabase(migrateCtx, db, exts, true, log.Printf, false)
		cancel()
		if err != nil {
			return fmt.Errorf("migrate on start: %w", err)
		}
	}
	// A build that reads a column the database lacks fails here, not as a 404
	// on every page at the first query.
	if err := operationrepo.VerifySchema(ctx, db); err != nil {
		return fmt.Errorf("schema check: %w", err)
	}
	if err := checkProductSchema(ctx, db, exts); err != nil {
		return err
	}
	// The install identity ties this database to this data directory. A
	// mismatch does not stop the server, but maintenance commands refuse.
	if err := storageapp.EnsureInstallIdentity(ctx, db, cfg.DataDir); err != nil {
		log.Printf("WARNING: INSTALL IDENTITY: %v. This server keeps serving", err)
	}
	return nil
}

// applySavedSetup reads the hostnames saved by the setup page into cfg; they
// win over the environment, which a catalog container may mount read-only.
// With neither, it returns the setup handler the instance serves alone.
func applySavedSetup(cfg *config.Config, db *sql.DB, ed *edition.Edition, productInstanceURL string) (*setup.SetupHandler, error) {
	savedDomain, savedContent, err := operations.InstanceConfigured(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("read instance config: %v", err)
	}
	if savedDomain != "" {
		cfg.SiteDomain, cfg.ContentHost = savedDomain, savedContent
		cfg.PublicBaseURL = "https://" + savedDomain
		// Addresses stay under the saved domain: a box set up here has one.
		cfg.SiteBaseDomain = savedDomain
		log.Printf("configured by setup: %s / %s", savedDomain, savedContent)
		return nil, nil
	}
	if cfg.SiteDomainSet {
		return nil, nil
	}
	return setup.NewSetupHandler(db, cfg.SetupPublicAPI, cfg.SetupPassword, cfg.DataDir, ed, productInstanceURL), nil
}

// configurePresentation sets the instance hosts and support contact the pages
// and assets render, and returns the website-deploy skill version.
func configurePresentation(cfg config.Config, ed *edition.Edition) (string, error) {
	// Before anything can serve an asset: the skills zips are built once and
	// cached for the process lifetime, so the rewriter has to exist first.
	web.SetInstanceHosts(cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.HandoutBase())
	// The hosted services keep their own support address. Elsewhere it is
	// SUPPORT_EMAIL, else a safe IDLE_REPLY_TO, else whoever runs the server.
	if cfg.SiteDomain != "simple-host.app" && "https://"+cfg.SiteDomain != ed.Presentation().CanonicalBaseURL {
		contact := "whoever runs this server"
		// IDLE_REPLY_TO's own validator is looser than SUPPORT_EMAIL's, so a
		// value unsafe to splice into a page is logged and treated as absent.
		if r := cfg.Limits.IdleReplyTo; r != "" && r != "support@simple-host.app" {
			if config.IsSafeContactAddress(r) {
				contact = r
			} else {
				log.Printf("warning: IDLE_REPLY_TO=%q is not safe to show as a contact address; using it only for idle-cleanup email, not as the support contact", r)
			}
		}
		if e := cfg.Limits.SupportEmail; e != "" {
			contact = e
		}
		auth.SetSupportContact(contact, cfg.ResendAPIKey != "")
	}
	pluginVersion, err := web.PluginVersion()
	if v := ed.Presentation().PluginVersion; v != "" {
		pluginVersion, err = v, nil
	}
	if err != nil {
		return "", fmt.Errorf("read plugin.json version: %v", err)
	}
	log.Printf("website-deploy skill version: %s", pluginVersion)
	return pluginVersion, nil
}

// configureSignIn returns the mailer and sets the process-wide sign-in rules.
func configureSignIn(cfg config.Config, ed *edition.Edition, hosted bool) *email.ResendSender {
	mailer := email.NewResendSender(cfg.ResendAPIKey, cfg.MailFrom)
	mailer.SetCodeLifetime(config.Span(cfg.Limits.SigninCodeTTL))
	if hosted {
		mailer.SetProductName(ed.Presentation().ProductName)
	}
	if cfg.ResendAPIKey == "" {
		log.Printf("no RESEND_API_KEY: no email is sent, so sign-in codes (/v1/auth) are off; accounts use keys the admin issues")
	}
	web.SetEmailSignIn(cfg.EmailSignInEnabled())
	httpx.SetTrustedProxies(cfg.TrustedProxyCIDRs)
	if len(cfg.TrustedProxyCIDRs) > 0 {
		log.Printf("trusted proxies: %d network(s) (TRUSTED_PROXY_CIDRS); X-Forwarded-For is believed only from them", len(cfg.TrustedProxyCIDRs))
	}
	if cfg.EmailSignIn == "off" {
		log.Printf("EMAIL_SIGNIN=off: email-code sign-in is off everywhere; sign in with a company account")
	}
	if names := cfg.EnabledVisitorProviders(); len(names) == 0 {
		log.Printf("warning: no OAuth providers configured; visitor Google sign-in disabled")
	} else {
		log.Printf("visitor OAuth enabled: %s", strings.Join(names, ", "))
	}
	if cfg.CompanySignInEnabled() {
		log.Printf("company sign-in: oidc issuer %s (label %q)", cfg.OIDCIssuer, cfg.OIDCLabel)
	}
	return mailer
}

// newHosts is the instance's address settings: which site a host names and
// which address a site has, shared by the site, access and data handlers.
func newHosts(cfg config.Config, db *sql.DB, disk sites.Disk, ed *edition.Edition) (*sites.Hosts, error) {
	hosts := sites.NewHosts(db, disk, ed, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.CustomDomainIP)
	hosts.SetPersonHosts(cfg.PersonHosts)
	hosts.SetCustomDomainIPAlso(cfg.CustomDomainIPAlso)
	hosts.SetSiteHosts(cfg.SiteHosts, cfg.SiteCertDir)
	hosts.SetSiteBase(cfg.SiteBaseDomain, cfg.SiteBaseMove)
	hosts.RequireFlatClean(context.Background(), cfg.TeamSiteFlatHosts)
	hosts.SetDomainCertDir(cfg.DomainCertDir)
	// The platform's other zones are never an owner's domain or family.
	if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Hostname() != "" {
		hosts.SetPlatformZones(append([]string{u.Hostname()}, cfg.EventDomains...)...)
		// Account sign-in (an API key) is served only on this address; every
		// other host this server answers on carries hosted pages (accountauth.go).
		log.Printf("account sign-in: served only on %s (visitor sign-in on every site's own address)", u.Hostname())
	} else if strings.TrimSpace(cfg.PublicBaseURL) != "" {
		// A base URL with no host would silently switch the gate off.
		return nil, fmt.Errorf("PUBLIC_BASE_URL %q has no host; set it to https://<your app address>", cfg.PublicBaseURL)
	} else {
		hosts.SetPlatformZones(cfg.EventDomains...)
	}
	return hosts, nil
}

// configureSites applies the hosting settings to the site, access and data
// handlers before their routes are registered.
func configureSites(cfg config.Config, db *sql.DB, site *siteshttp.SiteHandler, access *accesshttp.AccessHandler, data *storageapp.Handler, user *accesshttp.UserHandler) error {
	access.SetVisitorSignIn(cfg.ResendAPIKey != "", cfg.EnabledVisitorProviders())
	site.SetVisitorSignIn(cfg.ResendAPIKey != "", cfg.EnabledVisitorProviders())
	site.SetTeamSiteFlatHosts(cfg.TeamSiteFlatHosts)
	// auth.js serves pages under every base while addresses move.
	web.SetSiteBaseText(site.ServedBases())
	// Address families: *.<domain> for every site of an account (familyhost.go).
	site.SetAddressFamilies(cfg.FamilyCertDir)
	site.SetIdleCleanup(cfg.IdleCleanup)
	site.SetIdleExempt(cfg.IdleCleanupExemptHandles, cfg.ReviewAccountEmail)
	site.SetPublicBaseURL(cfg.PublicBaseURL)
	data.SetPublicBaseURL(cfg.PublicBaseURL)
	data.SetTurnstile(cfg.TurnstileSiteKey, cfg.TurnstileSecret, cfg.PublicBaseURL)
	if data.SpamCheckEnabled() {
		log.Printf("spam check: Turnstile on; storage rules may use auth.checked")
	} else {
		log.Printf("spam check: off (no TURNSTILE_SITE_KEY and secret); storage rules cannot use auth.checked")
	}
	if err := access.SetPasscodeKey(cfg.PasscodeEncKey); err != nil {
		return fmt.Errorf("config: %v", err)
	}
	// Data-only sweep of legacy notification secrets, never a schema migration.
	// Legacy plaintext rows keep working, so a failed sweep is logged, not fatal.
	sealCtx, sealCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if count, err := data.SealNotificationSecrets(sealCtx, db); err != nil {
		log.Printf("notifications: sealing stored secrets failed; retried on next start")
	} else if data.NotificationsReady() {
		log.Printf("notifications: sealed %d stored secrets", count)
	}
	sealCancel()
	if cfg.Limits.SitePasscodes && cfg.PasscodeEncKey == "" && !site.SharedOrigin() {
		log.Printf("site passcodes: PASSCODE_ENC_KEY is not set, so no site can get a passcode")
	}
	// One namespace across every domain people's addresses live under.
	accountrepo.SetPlatformDomains(site.HandoutBase(), site.ServedBases()...)
	log.Printf("person hosts: %s; site hosts: %s", cfg.PersonHosts, cfg.SiteHosts)
	if bases := site.ServedBases(); len(bases) > 1 {
		log.Printf("site base: %s; addresses under %s (new accounts: %s)", cfg.SiteBaseMove, strings.Join(bases, " and "), site.HandoutBase())
	}
	return nil
}

// registerEventDomains serves event hostnames for hackathon organisers. It is
// off unless a DNS token and at least one domain are configured, so a
// self-hosted instance never hands out names under a domain it does not control.
func registerEventDomains(cfg config.Config, db *sql.DB, ed *edition.Edition, mux *http.ServeMux, authMW func(http.Handler) http.Handler) *events.Handler {
	if cfg.EventDNSToken == "" || len(cfg.EventDomains) == 0 {
		return nil
	}
	deps := sites.Deps{EventDNS: eventdns.NewVercel(cfg.EventDNSToken, cfg.EventDNSTeamID), EventProbe: eventdns.NewLiveProbe(), EventDomains: cfg.EventDomains, EventTTL: ed.Limits().EventTTL, EventMaxClaims: ed.Limits().EventMaxClaims}
	peered := cfg.EventNamePeer != "" && !ed.Routes().HostedEvents
	if peered {
		// One name list with the hackathon platform.
		deps.EventNamePeer = eventdns.NewNamePeer(cfg.EventNamePeer)
	}
	ev := events.NewHandler(sites.New(db, nil, nil, 0, deps), cfg.EventDomains)
	if peered {
		namepeer.RegisterNamePeer(mux, cfg.EventDomains, ev.NameClaimed)
		log.Printf("event hostnames: names checked with %s", cfg.EventNamePeer)
	}
	ev.Register(mux, authMW)
	log.Printf("event hostnames enabled under: %s", strings.Join(cfg.EventDomains, ", "))
	return ev
}

// paperMetrics collects latency per route class, process samples and
// aggregate snapshots for the research papers. PAPER_METRICS=off returns nil.
func paperMetrics(cfg config.Config, db *sql.DB, hosted bool, data *storageapp.Handler, parts extension.Parts, mux *http.ServeMux, authMW func(http.Handler) http.Handler) *papermetrics.Collector {
	if !papermetrics.Enabled() {
		return nil
	}
	runConfig := map[string]any{"limits": cfg.Limits, "events": map[bool]string{false: "off", true: "hosted"}[hosted], "person_hosts": cfg.PersonHosts, "site_hosts": cfg.SiteHosts, "paper_metrics": true}
	apex := []string{cfg.SiteDomain}
	if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Hostname() != "" {
		apex = append(apex, u.Hostname())
	}
	paper := papermetrics.New(db, cfg.DataDir, apex...).WithRunConfig(runConfig).WithCommit(buildinfo.Commit)
	paper.KVFileStats = data.KVFileStats
	if parts.ProductMetrics != nil {
		parts.ProductMetrics(paper)
	}
	mux.Handle("GET /v1/admin/paper-metrics", authMW(paper.Export(func(r *http.Request) (bool, bool) {
		u := auth.GetUser(r.Context())
		return u != nil, u != nil && u.IsAdmin
	})))
	return paper
}

// analyticsIngester tails the nginx analytics log into daily aggregates. It is
// off (nil) unless ANALYTICS_LOG is set.
func analyticsIngester(cfg config.Config, db *sql.DB, site *siteshttp.SiteHandler, access *accesshttp.AccessHandler) *analytics.Ingester {
	if cfg.AnalyticsLog == "" {
		return nil
	}
	ingester := analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain).
		WithBases(cfg.SiteBaseDomain).
		WithSalt(cfg.AnalyticsSalt).
		WithRetentionDays(cfg.Limits.AnalyticsRetention).
		WithItemCaps(cfg.Limits.AnalyticsPagesDay, cfg.Limits.AnalyticsRefsDay)
	// The owner's own visits are told apart by marks the handlers write
	// (ownermarks.go); an install without analytics writes none.
	marks := operations.NewOwnerMarks(db, cfg.AdminAPIKey, cfg.AnalyticsSalt)
	site.SetOwnerMarks(marks)
	access.SetOwnerMarks(marks)
	log.Printf("analytics ingester enabled: %s", cfg.AnalyticsLog)
	return ingester
}

// publicHTTP dispatches by host, outermost first: an address family's
// <label>.<domain> (never under a platform domain), the site base, a claimed
// <name>.<SITE_DOMAIN> served like a custom domain, <site>.<handle> site hosts,
// person hosts, and the legacy 301 for every other single-label name. app
// serves everything else, including /v1/push/sw.js.
func publicHTTP(site *siteshttp.SiteHandler, app http.Handler, paper *papermetrics.Collector, parts extension.Parts) http.Handler {
	served := site.FamilyHosts(app, site.SiteBaseHosts(site.BoundSubdomains(app, site.SiteHosts(app, site.PersonHosts(app, site.LegacyHostRedirect(app))))))
	if paper == nil {
		return served
	}
	if parts.PaperRecorder != nil {
		served = parts.PaperRecorder(served)
	}
	return paper.Wrap(served)
}
