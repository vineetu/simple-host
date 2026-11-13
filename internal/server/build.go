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
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // Container images have no zoneinfo.

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/analytics"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/eventdns"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/imagescan"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/papermetrics"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	legacyrepo "github.com/vsriram/simple-host/internal/repository/legacy"
	operationrepo "github.com/vsriram/simple-host/internal/repository/operations"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/workers"
)

// App is the wired program, before any workers or listeners start.
type App struct {
	Mux       *http.ServeMux
	Handler   http.Handler
	Workers   *workers.Set
	Close     func()
	setupDone <-chan struct{}
}

// Build constructs the program in startup order. Close releases its dependencies;
// on failure Build releases every dependency it has already opened.
func Build(cfg config.Config, exts []*extension.Extension) (_ *App, err error) {
	if len(exts) > 1 {
		return nil, fmt.Errorf("at most one extension is supported")
	}
	var ext *extension.Extension
	if len(exts) == 1 {
		ext = exts[0]
	}

	if changed := cfg.Limits.Changed(); len(changed) > 0 {
		log.Printf("limits changed from the defaults: %s", strings.Join(changed, " "))
	}
	for _, w := range cfg.Limits.Warnings(os.Environ()) {
		log.Printf("WARNING: %s", w)
	}

	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)
	closeDeps := func() { db.Close() }
	defer func() {
		if err != nil {
			closeDeps()
		}
	}()

	diskStorage, err := storage.NewDiskStorage(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("create disk storage: %v", err)
	}

	mux := http.NewServeMux()

	// MIGRATE_ON_START=true (Kubernetes, where no install command runs before
	// the pod): build an empty database from the embedded schema.sql and apply
	// every pending migration, under the migrate advisory lock, before the
	// schema check below. A database that is still starting is waited for.
	if cfg.MigrateOnStart {
		log.Printf("MIGRATE_ON_START: building the schema if the database is empty, then applying pending migrations")
		if err := waitForDB(db, 2*time.Minute); err != nil {
			return nil, fmt.Errorf("migrate on start: database not reachable: %v", err)
		}
		migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 15*time.Minute)
		migrateErr := migrateDatabase(migrateCtx, db, true, log.Printf)
		cancelMigrate()
		if migrateErr != nil {
			return nil, fmt.Errorf("migrate on start: %v", migrateErr)
		}
	}

	// Before anything else touches the database: a build that reads a column the
	// database does not have fails at the first query, not at startup, which
	// looks like a 404 on every page rather than a deployment mistake.
	if err := operationrepo.VerifySchema(context.Background(), db); err != nil {
		return nil, fmt.Errorf("schema check: %v", err)
	}
	// Hosted events (EVENTS=hosted, simple-hack.app): set before any route is
	// registered, because the dashboard, the chrome and the host dispatch read it.
	ed := edition.ForKindWithLimits(edition.Host, cfg.Limits)
	if ext != nil && ext.Edition != nil {
		ed = ext.Edition(cfg.Limits)
	}
	hosted := ed.Routes().HostedEvents
	// hackInstanceURL is this Hack instance's own public address (the asset
	// pipeline's inkBaseURL, assets.go): captured here, before setup mode
	// below may rewrite cfg.PublicBaseURL to a saved domain, so construction
	// always sees the originally configured address.
	var hackInstanceURL string
	if hosted {
		if err := operationrepo.VerifyHackSchema(context.Background(), db); err != nil {
			return nil, fmt.Errorf("schema check (EVENTS=hosted): %v", err)
		}
		hackInstanceURL = cfg.PublicBaseURL
		log.Printf("hosted events: on (the hackathon platform at %s)", cfg.PublicBaseURL)
	}

	if ext != nil && ext.Early != nil {
		// EarlyHooks is empty until the accounts hook slices add their fields.
		ext.Early(extension.EarlyDeps{DB: db, Config: cfg, Edition: ed})
	}

	// A box installed from a provider's catalog boots knowing nothing about
	// where it lives. Rather than serve a broken product on an address nobody
	// configured, it serves one setup page and nothing else until somebody
	// answers. Settings chosen there are read back here on the next start,
	// ahead of the environment, because the process cannot rewrite a file its
	// container mounted read-only but can always write to its own database.
	if savedDomain, savedContent, err := handler.InstanceConfigured(context.Background(), db); err != nil {
		return nil, fmt.Errorf("read instance config: %v", err)
	} else if savedDomain != "" {
		cfg.SiteDomain, cfg.ContentHost = savedDomain, savedContent
		cfg.PublicBaseURL = "https://" + savedDomain
		// Addresses stay under the saved domain: a box set up here has one.
		cfg.SiteBaseDomain = savedDomain
		log.Printf("configured by setup: %s / %s", savedDomain, savedContent)
	} else if !cfg.SiteDomainSet {
		setup := handler.NewSetupHandler(db, cfg.SetupPublicAPI, cfg.SetupPassword, cfg.DataDir, ed, hackInstanceURL)
		setup.Register(mux)
		log.Printf("SETUP MODE: no hostname configured; serving the setup page on %s", net.JoinHostPort(cfg.BindAddr, cfg.Port))
		return &App{Mux: mux, Handler: mux, Workers: &workers.Set{}, Close: closeDeps, setupDone: setup.Done()}, nil
	}

	// Per-site cap. A plain operator setting with a plain default: MAX_ARCHIVE_MB
	// or 100 MB. It used to be derived from a headcount answered at setup, until
	// the sites on this instance were measured — median 25 KB against that same
	// 100 MB cap — and every number derived from the cap turned out to be wrong
	// by three orders of magnitude. What the disk is really holding is reported
	// by /v1/admin/usage instead of predicted here.
	log.Printf("per-site limit: %d MB", handler.SiteLimit()>>20)

	// Ensure a real admin user row exists so the admin key can own sites.
	adminUserID, err := legacyrepo.EnsureAdminUser(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("ensure admin user: %v", err)
	}
	accountsService := accountapp.New(db)
	authMW := auth.Middleware(cfg.AdminAPIKey, adminUserID, accountsService, ed.Limits().KeyIdleExpiry)

	// Stale-skill notice middleware. Compares the X-Skill-Version header
	// against the bundled plugin.json version and injects a `_notice`
	// field into JSON responses when the caller is stale or version-
	// unaware. Scoped structurally — only routes wrapped via this param
	// receive it; state endpoints, static serving, skill downloads, and
	// health probes are deliberately left alone.
	// Before anything can serve an asset: the skills zips are built once and
	// cached for the process lifetime, so the rewriter has to exist first.
	handler.SetInstanceHosts(cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.HandoutBase())
	// Who a person writes to about their account: the hosted service's
	// support address on simple-host.app and simple-hack.app (both run by
	// the same team); elsewhere SUPPORT_EMAIL when the operator set one
	// (the clearly-named setting), else IDLE_REPLY_TO (the older, doubled-up
	// one) when that is set, else whoever runs the server.
	if cfg.SiteDomain != "simple-host.app" && cfg.SiteDomain != "simple-hack.app" {
		contact := "whoever runs this server"
		// IDLE_REPLY_TO's own validator is looser than SUPPORT_EMAIL's (it
		// predates this use and must keep accepting values already in use
		// for the idle-cleanup email): a value such as "ops\@example.org"
		// passes it but is not safe to splice into a page, a mailto: link
		// or a JSON-LD island (config.IsSafeContactAddress, same check
		// SUPPORT_EMAIL gets at config load). An IDLE_REPLY_TO that fails
		// it here is logged and treated as absent, exactly as an invalid
		// SUPPORT_EMAIL already is, rather than taking the install down.
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

	pluginVersion, err := handler.PluginVersion(ed.Presentation().HackAssets)
	if err != nil {
		return nil, fmt.Errorf("read plugin.json version: %v", err)
	}
	noticeMW := handler.NoticeMiddleware(pluginVersion)
	log.Printf("website-deploy skill version: %s", pluginVersion)

	mailer := email.NewResendSender(cfg.ResendAPIKey, cfg.MailFrom)
	mailer.SetCodeLifetime(config.Span(cfg.Limits.SigninCodeTTL))
	if hosted {
		mailer.SetProductName(ed.Presentation().ProductName)
	}
	if cfg.ResendAPIKey == "" {
		log.Printf("no RESEND_API_KEY: no email is sent, so sign-in codes (/v1/auth) and Submissions emails are off; accounts use keys the admin issues")
	}
	handler.SetEmailSignIn(cfg.EmailSignInEnabled())
	handler.SetTrustedProxies(cfg.TrustedProxyCIDRs)
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
	log.Printf("write auth mode: %s", cfg.WriteAuthMode)
	if cfg.LegacyStorageCutoff.IsZero() {
		log.Printf("legacy storage cutoff: off")
	} else {
		log.Printf("legacy storage cutoff: %s", cfg.LegacyStorageCutoff.UTC().Format("2006-01-02"))
	}

	handler.RegisterHealthRoutes(mux, db)
	userHandler := handler.NewUserHandler(db, accountsService, mailer, cfg.PublicBaseURL, ed)
	userHandler.SetPasswordSignIn(cfg.PasswordSignIn == "on", cfg.PasswordSelfSignup != "off")
	userHandler.Register(mux, authMW, noticeMW)
	siteHandler := handler.NewSiteHandler(ed, hackInstanceURL, db, diskStorage, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.CustomDomainIP, cfg.DeployScript, cfg.AdminAPIKey, cfg.PreviewAccounts, cfg.PreviewTTL, cfg.WriteAuthMode, adminUserID, mailer, userHandler.EmailLimiter())
	siteHandler.SetOperationsNetwork(operations.NewNetworkUsage(db, ed))
	siteHandler.SetOperationsReporting(operations.NewReporting(db))
	userHandler.ShareAssets(siteHandler)
	userHandler.SetFileUsage(siteHandler)
	siteHandler.SetVisitorSignIn(cfg.ResendAPIKey != "", cfg.EnabledVisitorProviders())
	siteHandler.SetPersonHosts(cfg.PersonHosts)
	siteHandler.SetCustomDomainIPAlso(cfg.CustomDomainIPAlso)
	siteHandler.SetSiteHosts(cfg.SiteHosts, cfg.SiteCertDir)
	siteHandler.SetTeamSiteFlatHosts(cfg.TeamSiteFlatHosts)
	siteHandler.SetSiteBase(cfg.SiteBaseDomain, cfg.SiteBaseMove, cfg.SiteBaseCertDir)
	// auth.js serves pages under every base while addresses move.
	handler.SetSiteBaseText(siteHandler.ServedBases())
	siteHandler.SetDomainCerts(cfg.DomainCertDir)
	// Address families: *.<domain> for every site of an account (familyhost.go).
	siteHandler.SetAddressFamilies(cfg.FamilyCertDir)
	// The platform's other zones are never an owner's domain or family.
	if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Hostname() != "" {
		siteHandler.SetPlatformZones(append([]string{u.Hostname()}, cfg.EventDomains...)...)
		// Account sign-in (an API key) is served only on this address; every
		// other host this server answers on carries hosted pages (accountauth.go).
		log.Printf("account sign-in: served only on %s (visitor sign-in on every site's own address)", u.Hostname())
	} else if strings.TrimSpace(cfg.PublicBaseURL) != "" {
		// A base URL with no host would silently switch the gate off.
		return nil, fmt.Errorf("PUBLIC_BASE_URL %q has no host; set it to https://<your app address>", cfg.PublicBaseURL)
	} else {
		siteHandler.SetPlatformZones(cfg.EventDomains...)
	}
	siteHandler.SetIdleCleanup(cfg.IdleCleanup, cfg.IdleCleanupMaxEmails)
	siteHandler.SetSavedData(cfg.Limits.SavedData)
	siteHandler.SetLegacyStorageCutoff(cfg.LegacyStorageCutoff)
	siteHandler.SetIdleExempt(cfg.IdleCleanupExemptHandles, cfg.ReviewAccountEmail)
	siteHandler.SetPublicBaseURL(cfg.PublicBaseURL)
	siteHandler.SetTurnstile(cfg.TurnstileSiteKey, cfg.TurnstileSecret, cfg.PublicBaseURL)
	if siteHandler.SpamCheckEnabled() {
		log.Printf("spam check: Turnstile on; the comments preset is offered")
	} else {
		log.Printf("spam check: off (no TURNSTILE_SITE_KEY and secret); the comments preset is not offered")
	}
	if err := siteHandler.SetPasscodeKey(cfg.PasscodeEncKey); err != nil {
		return nil, fmt.Errorf("config: %v", err)
	}
	// Data-only sweep of legacy notification secrets, never a schema migration.
	// Legacy plaintext rows keep working, so a failed sweep is logged, not fatal.
	sealCtx, sealCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if count, err := siteHandler.SealNotificationSecrets(sealCtx, db); err != nil {
		log.Printf("notifications: sealing stored secrets failed; retried on next start")
	} else if siteHandler.NotificationsReady() {
		log.Printf("notifications: sealed %d stored secrets", count)
	}
	sealCancel()
	if cfg.Limits.SitePasscodes && cfg.PasscodeEncKey == "" && !siteHandler.SharedOrigin() {
		log.Printf("site passcodes: PASSCODE_ENC_KEY is not set, so no site can get a passcode")
	}
	userHandler.SetPublicPage(siteHandler.PersonPageURL)
	userHandler.SetAddressState(siteHandler.AddressState)
	// One namespace across every domain people's addresses live under.
	accountrepo.SetPlatformDomains(siteHandler.HandoutBase(), siteHandler.ServedBases()...)
	log.Printf("person hosts: %s; site hosts: %s", cfg.PersonHosts, cfg.SiteHosts)
	if bases := siteHandler.ServedBases(); len(bases) > 1 {
		log.Printf("site base move: %s; addresses under %s (handed out: %s)", cfg.SiteBaseMove, strings.Join(bases, " and "), siteHandler.HandoutBase())
	}
	siteHandler.Register(mux, authMW, noticeMW)
	handler.SetInstanceNote(handler.InstanceFacts{
		SiteDomain: cfg.SiteDomain, BaseDomain: siteHandler.HandoutBase(), ContentHost: cfg.ContentHost,
		SharedOrigin: siteHandler.SharedOrigin(),
		SignInEmail:  cfg.EmailSignInEnabled(), SignInProviders: cfg.EnabledVisitorProviders(), OIDCLabel: cfg.OIDCLabel,
		EmailCapable: cfg.ResendAPIKey != "",
		Contact:      auth.SupportContact,
	})
	// Take-down markers on disk follow the database (suspend.go).
	siteHandler.SyncSuspendMarkers(context.Background())
	oauthHandler := handler.NewOAuthHandler(db, accountsService, OAuthSettings(cfg, accountsService), ed)
	oauthHandler.ShareAssets(siteHandler)
	oauthHandler.SetPersonSiteResolver(siteHandler.PersonReturnSite)
	oauthHandler.SetPasscodeCheck(siteHandler.PasscodeLetsIn)
	siteHandler.SetSSOHop(oauthHandler)
	oauthHandler.SetSiteBases(siteHandler.ServedBases(), siteHandler.SameUserHost)
	oauthHandler.Register(mux)
	// The connector: OAuth 2.1 authorization server + remote MCP endpoint.
	// Tool calls are served into the bare mux, as the person, so they meet the
	// same checks as the REST call they stand for.
	// Deploy-only keys are refused everywhere but the deploy routes, in this
	// one gate (internal/auth/scope.go). The MCP server calls back into the
	// gated mux, so its tools follow the same table.
	scoped := auth.ScopeGate(accountsService, mux, ed.Identity().TeamKeys, ed.Limits().KeyIdleExpiry)
	if ext != nil && ext.Gate != nil {
		scoped = ext.Gate(extension.GateDeps{Site: siteHandler, Edition: ed})(scoped)
	}
	gated := handler.APIErrors(mux, siteHandler.LegacyStorageCutoffGate(scoped), cfg.PublicBaseURL)
	// The connector's text names people's addresses under the base in use.
	mcp.SetAddressBase(cfg.SiteDomain, siteHandler.HandoutBase())
	connector := handler.NewConnectorHandler(db, cfg.PublicBaseURL, cfg.AdminAPIKey, cfg.SiteDomain, cfg.ContentHost, pluginVersion, gated, ed.Routes().HostedEvents, ed)
	connector.ShareAssets(siteHandler)
	connector.SetVisitorReferences(siteHandler)
	connector.SetVisitorTools(siteHandler, oauthHandler)
	connector.SetPreviewBases(siteHandler.ServedBases())
	connector.OfferSpamCheck(siteHandler.SpamCheckEnabled())
	// MCP Events and webhooks share the engine on Host and Hack.
	if events := siteHandler.EventsBackend(); events != nil {
		connector.SetEventsBackend(events)
	}
	connector.Register(mux, authMW)
	if err := connector.EnsureBuiltinClients(context.Background()); err != nil {
		log.Printf("connector: built-in clients: %v", err)
	}
	connector.EnableReviewerSignIn(cfg.ReviewAccountEmail, cfg.ReviewAccountPasswordHash)
	userHandler.SetReviewerEmail(cfg.ReviewAccountEmail)
	userHandler.SetPreviewAccounts(cfg.PreviewAccounts)
	siteHandler.SetWebPushAccounts(cfg.WebPushAccounts, cfg.WebPushAccountsAll)
	connector.SetSignInAlerts(userHandler.SignInAlerts())
	handler.RegisterOpenAIAppsChallenge(mux, cfg.OpenAIAppsChallenge)
	// The owner's product knowledge server (/brain/mcp, /v1/brain): hosted
	// simple-host.app only, on when BRAIN_DIR holds the bundle sh-brain-sync
	// builds. Never on the hackathon platform.
	if dir := os.Getenv("BRAIN_DIR"); dir != "" && !hosted {
		connector.EnableBrain(mux, authMW, filepath.Join(dir, "bundle.json"), mcp.OpenBrainStore(filepath.Join(dir, "bundle.json")))
	}
	handler.RegisterUIRoutes(mux, cfg.PublicBaseURL, siteHandler)
	handler.RegisterSkillsHub(mux, cfg.PublicBaseURL, siteHandler)

	// Image matching (SIM-200): uploaded images are checked against known
	// abuse hash sets through PhotoDNA. Nothing is built while the key is
	// empty, so a box without it is unchanged.
	var imageScan *operations.ImageScan
	if cfg.PhotoDNAKey != "" {
		client := imagescan.NewPhotoDNA(cfg.PhotoDNAKey)
		if cfg.PhotoDNAMatchURL != "" {
			client.MatchURL = cfg.PhotoDNAMatchURL
		}
		if cfg.PhotoDNAReportURL != "" {
			client.ReportURL = cfg.PhotoDNAReportURL
		}
		// The support contact is the default reporter and alert address; a
		// server whose contact is not an address gets a log line instead.
		contact := auth.SupportContact
		if !strings.Contains(contact, "@") {
			contact = cfg.Limits.IdleReplyTo
		}
		if !strings.Contains(contact, "@") {
			contact = ""
		}
		scanCfg := operations.ImageScanConfig{
			ProductName: ed.Presentation().ProductName,
			EvidenceDir: cfg.ImageScanEvidenceDir, AlertEmail: cfg.ImageScanAlertEmail,
			ReportOrg: cfg.ImageScanReportOrg, ReportName: cfg.ImageScanReportName, ReportEmail: cfg.ImageScanReportEmail,
			AutoReport: cfg.PhotoDNAAutoReport, PublicBaseURL: cfg.PublicBaseURL,
		}
		if scanCfg.EvidenceDir == "" {
			scanCfg.EvidenceDir = filepath.Join(cfg.DataDir, ".evidence")
		}
		if scanCfg.AlertEmail == "" {
			scanCfg.AlertEmail = contact
		}
		if scanCfg.ReportEmail == "" {
			scanCfg.ReportEmail = contact
		}
		// A report needs the organisation, a name, and an email on it.
		var reporter imagescan.Reporter
		if scanCfg.ReportOrg != "" && scanCfg.ReportName != "" && scanCfg.ReportEmail != "" {
			reporter = client
		}
		imageScan = operations.NewImageScan(db, diskStorage, client, reporter, mailer, scanCfg)
		siteHandler.SetImageScan(imageScan)

		handler.SetImageScanOn(true)
		reports := "reports from the admin page"
		if reporter == nil {
			reports = "reports off (set IMAGE_SCAN_REPORT_ORG, IMAGE_SCAN_REPORT_NAME, and a report email)"
		} else if cfg.PhotoDNAAutoReport {
			reports = "reports filed automatically"
		}
		alerts := "alerts to " + scanCfg.AlertEmail
		if scanCfg.AlertEmail == "" {
			alerts = "no alert address (log only)"
		}
		log.Printf("image matching on (photodna); %s; %s; evidence in %s", reports, alerts, scanCfg.EvidenceDir)
	}

	var parts extension.Parts
	if ext != nil && ext.Wire != nil {
		parts, err = ext.Wire(extension.Deps{
			DB: db, Disk: diskStorage, Mail: mailer, Accounts: accountsService,
			Site: siteHandler, Connector: connector, ImageScan: imageScan,
			AuthMW: authMW, Mux: mux, Config: cfg, Edition: ed,
		})
		if err != nil {
			return nil, fmt.Errorf("wire extension %s: %w", ext.Name, err)
		}
	}

	// Event hostnames for hackathon organisers. Off unless a DNS token and at
	// least one domain are configured, so a self-hosted instance never tries to
	// hand out names under a domain it does not control.
	var eventDomains *handler.EventDomainHandler
	if cfg.EventDNSToken != "" && len(cfg.EventDomains) > 0 {
		eventSvc := sites.New(db, nil, nil, 0, sites.Deps{EventDNS: eventdns.NewVercel(cfg.EventDNSToken, cfg.EventDNSTeamID), EventProbe: eventdns.NewLiveProbe(), EventDomains: cfg.EventDomains, EventTTL: ed.Limits().EventTTL, EventMaxClaims: ed.Limits().EventMaxClaims})
		ev := handler.NewEventDomainHandler(eventSvc, cfg.EventDomains)
		if cfg.EventNamePeer != "" && !hosted {
			// One name list with the hackathon platform (hack_mode.go).
			ev.SetNamePeer(handler.NamePeerClient(cfg.EventNamePeer))
			handler.RegisterNamePeer(mux, cfg.EventDomains, ev.NameClaimed)
			log.Printf("event hostnames: names checked with %s", cfg.EventNamePeer)
		}
		ev.Register(mux, authMW)
		eventDomains = ev
		log.Printf("event hostnames enabled under: %s", strings.Join(cfg.EventDomains, ", "))
	}

	// Per-endpoint API analytics for the admin page: every /v1/* API call is
	// counted (route, status, caller IP + geo) into daily aggregates.
	// Caller location comes from local DB-IP files only (no network lookup);
	// the watcher picks up the monthly refresh without a restart.
	geo := geoip.Open(cfg.GeoIPDir)
	closeDeps = func() { geo.Close(); db.Close() }

	// Sign-in/sign-up country block (SIGNUP_BLOCKED_COUNTRIES): local lookup
	// only, off by default. A listed country refuses every email-code and
	// OAuth sign-in or sign-up attempt, new account or existing; an API key
	// keeps working regardless of where a request comes from, since that is
	// not a sign-in. See internal/handler/signupgeo.go.
	signupGeoBlock := accountapp.NewSignupGeoBlock(geo, cfg.SignupBlockedCountries)
	userHandler.SetSignupGeoBlock(signupGeoBlock)
	siteHandler.SetSignupGeoBlock(signupGeoBlock)
	oauthHandler.SetSignupGeoBlock(signupGeoBlock)
	if len(cfg.SignupBlockedCountries) > 0 {
		log.Printf("signup geo-block: sign-in and sign-up refused from %s; API keys and site viewing unaffected", strings.Join(cfg.SignupBlockedCountries, ", "))
	}

	apiMetrics := handler.NewAPIMetrics(operations.NewAPIMetrics(db, geo, ed))
	// Requests that match no API route are not counted; calls from this box
	// (loopback, its own public address) are counted apart.
	apiMetrics.SetRouting(mux, append([]string{cfg.CustomDomainIP}, cfg.CustomDomainIPAlso...)...)
	mux.Handle("GET /v1/admin/api-analytics", authMW(http.HandlerFunc(apiMetrics.AdminSummary)))
	mux.Handle("GET /v1/admin/growth", authMW(http.HandlerFunc(apiMetrics.AdminGrowth)))

	// Paper metrics (SIM-335): latency per route class, process samples and
	// daily/weekly aggregate snapshots for the research papers; aggregates
	// only. PAPER_METRICS=off turns it off. docs/paper-metrics.md.
	var paper *papermetrics.Collector
	paperOn := papermetrics.Enabled()
	paperConfig := map[string]any{"limits": cfg.Limits, "events": cfg.Events, "write_auth_mode": cfg.WriteAuthMode, "person_hosts": cfg.PersonHosts, "site_hosts": cfg.SiteHosts, "paper_metrics": paperOn}
	if paperOn {
		apex := []string{cfg.SiteDomain}
		if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Hostname() != "" {
			apex = append(apex, u.Hostname())
		}
		paper = papermetrics.New(db, cfg.DataDir, apex...).WithRunConfig(paperConfig).WithCommit(buildinfo.Commit)
		if parts.PaperHack != nil {
			parts.PaperHack(paper)
		}
		mux.Handle("GET /v1/admin/paper-metrics", authMW(paper.Export(func(r *http.Request) (bool, bool) {
			u := auth.GetUser(r.Context())
			return u != nil, u != nil && u.IsAdmin
		})))
	}

	// Server-side visitor analytics: tail the nginx analytics log into daily
	// aggregates. Off unless ANALYTICS_LOG is set (safe default for local dev).
	var ingester *analytics.Ingester
	if cfg.AnalyticsLog != "" {
		ingester = analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain).
			WithBases(cfg.SiteBaseDomain).
			WithSalt(cfg.AnalyticsSalt).
			WithRetentionDays(cfg.Limits.AnalyticsRetention).
			WithItemCaps(cfg.Limits.AnalyticsPagesDay, cfg.Limits.AnalyticsRefsDay)
		// The owner's own visits are told apart by marks the handlers write
		// (ownermarks.go); an install without analytics writes none.
		siteHandler.SetOwnerMarks(operations.NewOwnerMarks(db, cfg.AdminAPIKey, cfg.AnalyticsSalt))
		log.Printf("analytics ingester enabled: %s", cfg.AnalyticsLog)
	}
	siteHandler.SetTrafficLog(cfg.AnalyticsLog != "")
	siteHandler.SetNetworkUsage(cfg.NetworkInterface)

	// An address family's <label>.<domain> is its account's site (familyhost.go;
	// never under a platform domain, so it goes first). A claimed
	// <name>.<SITE_DOMAIN> is served like a custom domain (its files
	// at the root, /v1 same-origin); <site>.<handle>.<SITE_DOMAIN> is a site's
	// own address when SITE_HOSTS is on (sitehost.go); an account's handle is
	// its own address when PERSON_HOSTS is on (personhost.go); every other single-label name
	// keeps the legacy 301 to its path URL.
	app := handler.SecurityHeaders(handler.CORS(apiMetrics.Wrap(connector.BearerAuth(gated))))
	// SIM-360's service worker is served at /v1/push/sw.js (see
	// notify_push.go's pushSWRoute), an ordinary route on the same
	// mux every /v1/... path already reaches regardless of which
	// host-dispatcher below runs first -- no separate wrapping
	// needed (that was the old, root-path ServePushSW's whole
	// reason for existing, and the problem it caused: FIX 2/11,
	// Codex round 2).
	served := siteHandler.FamilyHosts(app, siteHandler.SiteBaseHosts(siteHandler.BoundSubdomains(app, siteHandler.SiteHosts(app, siteHandler.PersonHosts(app, siteHandler.LegacyHostRedirect(app))))))
	if paper != nil {
		if parts.PaperRecorder != nil {
			served = parts.PaperRecorder(served)
		}
		served = paper.Wrap(handler.WithPaperRecorder(served, papermetrics.RequestRecorder{}))
	}

	var paperWorker *operations.PaperMetrics
	if paper != nil {
		paperWorker = operations.NewPaperMetrics(paper)
	}
	var analyticsWorker *operations.AnalyticsIngest
	if ingester != nil {
		analyticsWorker = operations.NewAnalyticsIngest(ingester, 5*time.Minute)
	}
	workerSet := registerWorkers(workerDependencies{site: siteHandler, user: userHandler, oauth: oauthHandler, connector: connector, productWorkers: parts.Workers, image: imageScan, paper: paperWorker, metrics: apiMetrics.APIMetrics, geo: geo, analytics: analyticsWorker, eventDomains: eventDomains, siteSweep: parts.SiteSweep})
	siteHandler.SetWorkerStatus(workerSet.Status)
	return &App{Mux: mux, Handler: served, Workers: workerSet, Close: closeDeps}, nil
}
