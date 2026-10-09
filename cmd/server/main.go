package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/config"
	dbpkg "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/eventdns"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/storage"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "prune-versions" {
		os.Exit(runPruneVersions(os.Args[2:]))
	}
	// `simple-host oauth-client ...` manages hand-registered connector clients
	// (a ChatGPT GPT Action) and exits; it needs only DB_DSN.
	if len(os.Args) > 1 && os.Args[1] == "oauth-client" {
		os.Exit(runOAuthClientCommand(os.Args[2:]))
	}

	// `simple-host geoip-verify FILE...` — used by scripts/geoip-refresh.sh to
	// check a freshly downloaded database opens and answers before it is
	// swapped into place. Needs no config or database.
	if len(os.Args) > 1 && os.Args[1] == "review-account" {
		os.Exit(runReviewAccountCommand(os.Args[2:]))
	}
	// `simple-host move-site-base --from A --to B [--apply]` rewrites stored
	// free and retired names to a new base domain (movesitebase.go).
	if len(os.Args) > 1 && os.Args[1] == "move-site-base" {
		os.Exit(runMoveSiteBaseCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "geoip-verify" {
		os.Exit(geoipVerify(os.Args[2:]))
	}
	// `simple-host migrate [-status | -mark FILE]` applies db/migrations/ to the
	// database in DB_DSN and exits. The server below never does this itself.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrateCommand(os.Args[2:]))
	}
	// `simple-host api-growth-backfill [LOG...]` fills the admin page's API
	// growth counts from the traffic tables and old nginx logs (apigrowth.go).
	if len(os.Args) > 1 && os.Args[1] == "api-growth-backfill" {
		os.Exit(runAPIGrowthBackfill(os.Args[2:]))
	}
	// `simple-host settings --json` prints every setting (docs/advanced/).
	if len(os.Args) > 1 && os.Args[1] == "settings" {
		os.Exit(runSettingsCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		os.Exit(runVersionCommand())
	}

	log.Printf("%s", buildinfo.String())

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	// Every operational time and limit, before anything reads one.
	handler.ApplyLimits(cfg.Limits)
	if changed := cfg.Limits.Changed(); len(changed) > 0 {
		log.Printf("limits changed from the defaults: %s", strings.Join(changed, " "))
	}
	for _, w := range cfg.Limits.Warnings(os.Environ()) {
		log.Printf("WARNING: %s", w)
	}

	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer db.Close()

	diskStorage, err := storage.NewDiskStorage(cfg.DataDir)
	if err != nil {
		log.Fatalf("create disk storage: %v", err)
	}

	mux := http.NewServeMux()

	// Before anything else touches the database: a build that reads a column the
	// database does not have fails at the first query, not at startup, which
	// looks like a 404 on every page rather than a deployment mistake.
	if err := dbpkg.VerifySchema(context.Background(), db); err != nil {
		log.Fatalf("schema check: %v", err)
	}
	// Hosted events (EVENTS=hosted, simple-hack.app): set before any route is
	// registered, because the dashboard, the chrome and the host dispatch read it.
	hosted := cfg.Events == "hosted"
	if hosted {
		if err := dbpkg.VerifyHackSchema(context.Background(), db); err != nil {
			log.Fatalf("schema check (EVENTS=hosted): %v", err)
		}
		handler.SetHackMode(true)
		handler.SetHackInstanceURL(cfg.PublicBaseURL)
		handler.SetHackChrome(true)
		log.Printf("hosted events: on (the hackathon platform at %s)", cfg.PublicBaseURL)
	}

	// A box installed from a provider's catalog boots knowing nothing about
	// where it lives. Rather than serve a broken product on an address nobody
	// configured, it serves one setup page and nothing else until somebody
	// answers. Settings chosen there are read back here on the next start,
	// ahead of the environment, because the process cannot rewrite a file its
	// container mounted read-only but can always write to its own database.
	if savedDomain, savedContent, err := handler.InstanceConfigured(context.Background(), db); err != nil {
		log.Fatalf("read instance config: %v", err)
	} else if savedDomain != "" {
		cfg.SiteDomain, cfg.ContentHost = savedDomain, savedContent
		cfg.PublicBaseURL = "https://" + savedDomain
		// Addresses stay under the saved domain: a box set up here has one.
		cfg.SiteBaseDomain = savedDomain
		log.Printf("configured by setup: %s / %s", savedDomain, savedContent)
	} else if !cfg.SiteDomainSet {
		setup := handler.NewSetupHandler(db, cfg.SetupPublicAPI, cfg.SetupPassword, cfg.DataDir)
		setup.Register(mux)
		log.Printf("SETUP MODE: no hostname configured; serving the setup page on %s", net.JoinHostPort(cfg.BindAddr, cfg.Port))
		srv := &http.Server{Addr: net.JoinHostPort(cfg.BindAddr, cfg.Port), Handler: mux}
		errs := make(chan error, 1)
		go func() { errs <- srv.ListenAndServe() }()
		select {
		case err := <-errs:
			if err != nil && err != http.ErrServerClosed {
				log.Fatalf("setup server: %v", err)
			}
		case <-setup.Done():
			// Setup wrote its answers to the database. This process cannot use
			// them: it registered the setup page and nothing else, and the
			// hostnames and per-site cap are read at startup. So it stops, and
			// the supervisor starts it again into the configured instance.
			// Without this the organiser is told "Done", reloads, and is handed
			// the setup page for a second time.
			log.Printf("setup complete; restarting into the configured instance")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Printf("setup server shutdown: %v", err)
			}
		}
		return
	}

	// Per-site cap. A plain operator setting with a plain default: MAX_ARCHIVE_MB
	// or 100 MB. It used to be derived from a headcount answered at setup, until
	// the sites on this instance were measured — median 25 KB against that same
	// 100 MB cap — and every number derived from the cap turned out to be wrong
	// by three orders of magnitude. What the disk is really holding is reported
	// by /v1/admin/usage instead of predicted here.
	log.Printf("per-site limit: %d MB", handler.SiteLimit()>>20)

	// Ensure a real admin user row exists so the admin key can own sites.
	adminUserID, err := dbpkg.EnsureAdminUser(context.Background(), db)
	if err != nil {
		log.Fatalf("ensure admin user: %v", err)
	}
	authMW := auth.Middleware(cfg.AdminAPIKey, adminUserID, db)

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
	// support address only on simple-host.app; elsewhere IDLE_REPLY_TO when
	// the operator set one, or whoever runs the server.
	if cfg.SiteDomain != "simple-host.app" {
		contact := "whoever runs this server"
		if r := cfg.Limits.IdleReplyTo; r != "" && r != "support@simple-host.app" {
			contact = r
		}
		auth.SetSupportContact(contact, cfg.ResendAPIKey != "")
	}

	pluginVersion, err := handler.PluginVersion()
	if err != nil {
		log.Fatalf("read plugin.json version: %v", err)
	}
	noticeMW := handler.NoticeMiddleware(pluginVersion)
	log.Printf("website-deploy skill version: %s", pluginVersion)

	mailer := email.NewResendSender(cfg.ResendAPIKey, cfg.MailFrom)
	mailer.SetCodeLifetime(config.Span(cfg.Limits.SigninCodeTTL))
	if hosted {
		mailer.SetProductName("Simple Hack")
	}
	if cfg.ResendAPIKey == "" {
		log.Printf("no RESEND_API_KEY: no email is sent, so sign-in codes (/v1/auth) and Submissions emails are off; accounts use keys the admin issues")
	}

	if names := cfg.EnabledVisitorProviders(); len(names) == 0 {
		log.Printf("warning: no OAuth providers configured; visitor Google sign-in disabled")
	} else {
		log.Printf("visitor OAuth enabled: %s", strings.Join(names, ", "))
	}
	log.Printf("write auth mode: %s", cfg.WriteAuthMode)

	handler.RegisterHealthRoutes(mux, db)
	userHandler := handler.NewUserHandler(db, mailer, cfg.PublicBaseURL)
	userHandler.Register(mux, authMW, noticeMW)
	siteHandler := handler.NewSiteHandler(db, diskStorage, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.CustomDomainIP, cfg.DeployScript, cfg.AdminAPIKey, cfg.PreviewAccounts, cfg.PreviewTTL, cfg.WriteAuthMode, adminUserID, mailer, userHandler.EmailLimiter())
	userHandler.SetFileUsage(siteHandler)
	siteHandler.SetVisitorSignIn(cfg.ResendAPIKey != "", cfg.EnabledVisitorProviders())
	siteHandler.SetPersonHosts(cfg.PersonHosts)
	siteHandler.SetSiteHosts(cfg.SiteHosts, cfg.SiteCertDir)
	siteHandler.SetSiteBase(cfg.SiteBaseDomain, cfg.SiteBaseMove, cfg.SiteBaseCertDir)
	// auth.js serves pages under every base while addresses move.
	handler.SetSiteBaseText(siteHandler.ServedBases())
	siteHandler.SetDomainCerts(cfg.DomainCertDir)
	// Address families: *.<domain> for every site of an account (familyhost.go).
	siteHandler.SetAddressFamilies(cfg.FamilyCertDir)
	// The platform's other zones are never an owner's domain or family.
	if u, err := url.Parse(cfg.PublicBaseURL); err == nil && u.Hostname() != "" {
		siteHandler.SetPlatformZones(append([]string{u.Hostname()}, cfg.EventDomains...)...)
	} else {
		siteHandler.SetPlatformZones(cfg.EventDomains...)
	}
	siteHandler.SetIdleCleanup(cfg.IdleCleanup, cfg.IdleCleanupMaxEmails)
	siteHandler.SetSavedData(cfg.Limits.SavedData)
	siteHandler.SetIdleExempt(cfg.IdleCleanupExemptHandles, cfg.ReviewAccountEmail)
	siteHandler.SetPublicBaseURL(cfg.PublicBaseURL)
	if err := siteHandler.SetPasscodeKey(cfg.PasscodeEncKey); err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.Limits.SitePasscodes && cfg.PasscodeEncKey == "" && !siteHandler.SharedOrigin() {
		log.Printf("site passcodes: PASSCODE_ENC_KEY is not set, so no site can get a passcode")
	}
	userHandler.SetPublicPage(siteHandler.PersonPageURL)
	userHandler.SetAddressState(siteHandler.AddressState)
	// One namespace across every domain people's addresses live under.
	dbpkg.SetPlatformDomains(siteHandler.HandoutBase(), siteHandler.ServedBases()...)
	log.Printf("person hosts: %s; site hosts: %s", cfg.PersonHosts, cfg.SiteHosts)
	if bases := siteHandler.ServedBases(); len(bases) > 1 {
		log.Printf("site base move: %s; addresses under %s (handed out: %s)", cfg.SiteBaseMove, strings.Join(bases, " and "), siteHandler.HandoutBase())
	}
	siteHandler.Register(mux, authMW, noticeMW)
	handler.SetInstanceNote(handler.InstanceFacts{
		SiteDomain: cfg.SiteDomain, BaseDomain: siteHandler.HandoutBase(), ContentHost: cfg.ContentHost,
		SharedOrigin: siteHandler.SharedOrigin(),
		SignInEmail:  cfg.ResendAPIKey != "", SignInProviders: cfg.EnabledVisitorProviders(),
		Contact: auth.SupportContact,
	})
	// Take-down markers on disk follow the database (suspend.go).
	siteHandler.SyncSuspendMarkers(context.Background())
	oauthHandler := handler.NewOAuthHandler(db, cfg)
	oauthHandler.SetPersonSiteResolver(siteHandler.PersonReturnSite)
	oauthHandler.SetPasscodeCheck(siteHandler.PasscodeLetsIn)
	oauthHandler.SetSiteBases(siteHandler.ServedBases(), siteHandler.SameUserHost)
	oauthHandler.Register(mux)
	// The connector: OAuth 2.1 authorization server + remote MCP endpoint.
	// Tool calls are served into the bare mux, as the person, so they meet the
	// same checks as the REST call they stand for.
	// Deploy-only keys are refused everywhere but the deploy routes, in this
	// one gate (internal/auth/scope.go). The MCP server calls back into the
	// gated mux, so its tools follow the same table.
	gated := handler.HackLegacyStorageGate(auth.ScopeGate(db, mux))
	// The connector's text names people's addresses under the base in use.
	mcp.SetAddressBase(cfg.SiteDomain, siteHandler.HandoutBase())
	connector := handler.NewConnectorHandler(db, cfg.PublicBaseURL, cfg.AdminAPIKey, cfg.SiteDomain, cfg.ContentHost, pluginVersion, gated)
	connector.Register(mux, authMW)
	connector.EnableReviewerSignIn(cfg.ReviewAccountEmail, cfg.ReviewAccountPasswordHash)
	userHandler.SetReviewerEmail(cfg.ReviewAccountEmail)
	userHandler.SetPreviewAccounts(cfg.PreviewAccounts)
	connector.SetSignInAlerts(userHandler.SignInAlerts())
	handler.RegisterOpenAIAppsChallenge(mux, cfg.OpenAIAppsChallenge)
	connector.StartSweep(time.Hour)
	handler.RegisterUIRoutes(mux, cfg.PublicBaseURL, siteHandler)
	handler.RegisterSkillsHub(mux, cfg.PublicBaseURL)
	var hackSweep func(context.Context)
	var hackEvents *handler.HackHandler
	if hosted {
		hack := handler.NewHackHandler(db, cfg.PublicBaseURL, cfg.SiteDomain)
		hack.SetMailer(mailer)
		hackEvents = hack
		if cfg.EventNamePeer != "" {
			peer, zone := handler.NamePeerClient(cfg.EventNamePeer), cfg.SiteDomain
			hack.SetNamePeer(func(ctx context.Context, name string) (bool, error) { return peer(ctx, zone, name) })
			log.Printf("hosted events: event names checked with %s", cfg.EventNamePeer)
		}
		hack.SetInstanceUsage(handler.DirUsage(cfg.DataDir))
		// Team sites (M2): member keys act on one team site; the sweep pins
		// teams at their deadline, removes sites of removed teams and asks
		// for open events' certificates.
		hack.SetSites(siteHandler)
		auth.SetTeamKeys(true)
		connector.SetHackTeams(true)
		hackSweep = hack.RequestOpenEventCerts
		hack.Register(mux, authMW)
		hack.StartCleanup()
		handler.RegisterNamePeer(mux, []string{cfg.SiteDomain}, func(ctx context.Context, _ string, name string) (bool, error) {
			return hack.NameTaken(ctx, name)
		})
		siteHandler.SetHackEventPage(handler.HackEventPage(db, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady))
		siteHandler.SetHackScreenshot(handler.HackScreenshot(db, siteHandler.TeamSitesReady))
		handler.RegisterHackHome(mux)
		handler.RegisterHackPublic(mux, db, cfg.PublicBaseURL, siteHandler.TeamSiteURL, siteHandler.TeamSitesReady)
		handler.RegisterHackUI(mux)
	}

	// Event hostnames for hackathon organisers. Off unless a DNS token and at
	// least one domain are configured, so a self-hosted instance never tries to
	// hand out names under a domain it does not control.
	if cfg.EventDNSToken != "" && len(cfg.EventDomains) > 0 {
		ev := handler.NewEventDomainHandler(db, eventdns.NewVercel(cfg.EventDNSToken, cfg.EventDNSTeamID), cfg.EventDomains)
		if cfg.EventNamePeer != "" && !hosted {
			// One name list with the hackathon platform (hack_mode.go).
			ev.SetNamePeer(handler.NamePeerClient(cfg.EventNamePeer))
			handler.RegisterNamePeer(mux, cfg.EventDomains, ev.NameClaimed)
			log.Printf("event hostnames: names checked with %s", cfg.EventNamePeer)
		}
		ev.Register(mux, authMW)
		ev.StartSweep(1 * time.Hour)
		log.Printf("event hostnames enabled under: %s", strings.Join(cfg.EventDomains, ", "))
	}

	// Per-endpoint API analytics for the admin page: every /v1/* API call is
	// counted (route, status, caller IP + geo) into daily aggregates.
	// Caller location comes from local DB-IP files only (no network lookup);
	// the watcher picks up the monthly refresh without a restart.
	geo := geoip.Open(cfg.GeoIPDir)
	geo.Watch(time.Minute)

	// Sign-in/sign-up country block (SIGNUP_BLOCKED_COUNTRIES): local lookup
	// only, off by default. A listed country refuses every email-code and
	// OAuth sign-in or sign-up attempt, new account or existing; an API key
	// keeps working regardless of where a request comes from, since that is
	// not a sign-in. See internal/handler/signupgeo.go.
	userHandler.SetSignupGeoBlock(geo, cfg.SignupBlockedCountries)
	siteHandler.SetSignupGeoBlock(geo, cfg.SignupBlockedCountries)
	oauthHandler.SetSignupGeoBlock(geo, cfg.SignupBlockedCountries)
	if len(cfg.SignupBlockedCountries) > 0 {
		log.Printf("signup geo-block: sign-in and sign-up refused from %s; API keys and site viewing unaffected", strings.Join(cfg.SignupBlockedCountries, ", "))
	}

	apiMetrics := handler.NewAPIMetrics(db, geo)
	// Requests that match no API route are not counted; calls from this box
	// (loopback, its own public address) are counted apart.
	apiMetrics.SetRouting(mux, cfg.CustomDomainIP)
	mux.Handle("GET /v1/admin/api-analytics", authMW(http.HandlerFunc(apiMetrics.AdminSummary)))
	mux.Handle("GET /v1/admin/growth", authMW(http.HandlerFunc(apiMetrics.AdminGrowth)))

	// Server-side visitor analytics: tail the nginx analytics log into daily
	// aggregates. Off unless ANALYTICS_LOG is set (safe default for local dev).
	if cfg.AnalyticsLog != "" {
		analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain).
			WithBases(cfg.SiteBaseDomain).
			WithSalt(cfg.AnalyticsSalt).
			WithRetentionDays(cfg.Limits.AnalyticsRetention).
			WithItemCaps(cfg.Limits.AnalyticsPagesDay, cfg.Limits.AnalyticsRefsDay).
			Start(5 * time.Minute)
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
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, cfg.Port),
		Handler:           siteHandler.FamilyHosts(app, siteHandler.SiteBaseHosts(siteHandler.BoundSubdomains(app, siteHandler.SiteHosts(app, siteHandler.PersonHosts(app, siteHandler.LegacyHostRedirect(app)))))),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Ask the root issuer for each person's *.<handle>.<SITE_DOMAIN>
	// certificate (back-fill at boot, then a periodic safety net).
	siteHandler.StartSiteCertRequests(ctx, 10*time.Minute)
	siteHandler.StartHackSweep(ctx, time.Minute, hackSweep)
	if hackEvents != nil {
		hackEvents.StartEventCleanup(ctx)
		hackEvents.StartContentDelivery(ctx)
	}
	siteHandler.StartDeletedSitePurge(ctx, time.Hour)
	siteHandler.StartIdleCleanup(ctx)
	siteHandler.StartSavedDataSweep(ctx)
	siteHandler.StartSubmissionEmails(ctx)
	siteHandler.StartAddressFamilies(ctx)
	siteHandler.StartNetworkUsage(ctx)

	serverErr := make(chan error, 1)

	go func() {
		log.Printf("listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close failed: %v", closeErr)
		}
		os.Exit(1)
	}
}

func geoipVerify(files []string) int {
	if len(files) == 0 {
		log.Printf("usage: simple-host geoip-verify FILE.mmdb...")
		return 2
	}
	for _, f := range files {
		kind, err := geoip.Verify(f)
		if err != nil {
			log.Printf("geoip-verify: %s: %v", f, err)
			return 1
		}
		log.Printf("geoip-verify: %s ok (%s)", f, kind)
	}
	return 0
}
