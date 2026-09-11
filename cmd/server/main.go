package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	dbpkg "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/eventdns"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
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
		log.Printf("configured by setup: %s / %s", savedDomain, savedContent)
	} else if !cfg.SiteDomainSet {
		setup := handler.NewSetupHandler(db, cfg.SetupPublicAPI, cfg.SetupPassword, cfg.DataDir)
		setup.Register(mux)
		log.Printf("SETUP MODE: no hostname configured; serving the setup page on :%s", cfg.Port)
		srv := &http.Server{Addr: ":" + cfg.Port, Handler: mux}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("setup server: %v", err)
		}
		return
	}

	// The per-site cap chosen during setup, applied before anything can be
	// uploaded. Without this a restart returns an instance that was sized for
	// 2 MB sites to the 100 MB default, and the disk it was sized against is
	// then one participant away from full.
	if siteLimit, err := handler.LoadSiteLimit(context.Background(), db); err != nil {
		log.Fatalf("read per-site limit: %v", err)
	} else if siteLimit > 0 {
		handler.SetSiteLimit(siteLimit)
		log.Printf("per-site limit: %d MB (chosen at setup)", siteLimit>>20)
	} else if plan, auto := handler.AutoSiteLimit(cfg.DataDir); auto {
		// Recomputed every boot rather than saved. Persisting it would outrank a
		// later explicit MAX_ARCHIVE_MB, since a stored limit is read before the
		// environment is consulted — an operator who set a number would find it
		// silently ignored.
		handler.SetSiteLimit(plan.SiteBytes())
		log.Printf("per-site limit: %d MB (sized from disk) — %s", plan.SiteMB, plan.Explanation)
	} else {
		log.Printf("per-site limit: %d MB (default)", handler.SiteLimit()>>20)
	}

	// Ensure a real admin user row exists so the admin key can own sites.
	adminKey, err := auth.GenerateAPIKey()
	if err != nil {
		log.Fatalf("generate admin row key: %v", err)
	}
	adminUserID, err := dbpkg.EnsureAdminUser(context.Background(), db, adminKey)
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
	handler.SetInstanceHosts(cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget)

	pluginVersion, err := handler.PluginVersion()
	if err != nil {
		log.Fatalf("read plugin.json version: %v", err)
	}
	noticeMW := handler.NoticeMiddleware(pluginVersion)
	log.Printf("website-deploy skill version: %s", pluginVersion)

	mailer := email.NewResendSender(cfg.ResendAPIKey, cfg.MailFrom)
	if cfg.ResendAPIKey == "" {
		log.Printf("warning: RESEND_API_KEY not set; /v1/auth will fail until it is configured")
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
	siteHandler.Register(mux, authMW, noticeMW)
	handler.NewOAuthHandler(db, cfg).Register(mux)
	handler.RegisterUIRoutes(mux, cfg.PublicBaseURL, siteHandler)
	handler.RegisterSkillsHub(mux, cfg.PublicBaseURL)

	// Optional "create with AI" endpoint. Sign-in-gated + rate limited; only
	// enabled when the Grok sidecar (or another single OpenAI-compatible
	// endpoint) is configured. One provider, no fallbacks, no metered API keys.
	if cfg.LLMAPIKey != "" {
		handler.NewGenerateHandler(cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel, cfg.VisionAPIKey, cfg.VisionBaseURL, cfg.VisionModel).Register(mux, authMW)
		log.Printf("AI create endpoint enabled (/v1/generate, provider %s, %s, model %s; no fallback)", cfg.LLMProvider, cfg.LLMBaseURL, cfg.LLMModel)
	} else {
		log.Printf("no model backend set (LLM_API_KEY); /v1/generate (AI create) disabled")
	}

	// Voice input for the builder chat. Local speech-to-text, so this is CPU on
	// this box rather than a metered API; still sign-in-gated and rate limited,
	// because it is CPU anyone signed in can spend.
	if cfg.TranscribeURL != "" {
		handler.NewTranscribeHandler(cfg.TranscribeURL, cfg.TranscribeTicketSecret).Register(mux, authMW)
		log.Printf("voice input enabled (/v1/transcribe -> %s)", cfg.TranscribeURL)
	} else {
		log.Printf("TRANSCRIBE_URL unset; /v1/transcribe (voice input) disabled")
	}

	// Event hostnames for hackathon organisers. Off unless a DNS token and at
	// least one domain are configured, so a self-hosted instance never tries to
	// hand out names under a domain it does not control.
	if cfg.EventDNSToken != "" && len(cfg.EventDomains) > 0 {
		ev := handler.NewEventDomainHandler(db, eventdns.NewVercel(cfg.EventDNSToken, cfg.EventDNSTeamID), cfg.EventDomains)
		ev.Register(mux, authMW)
		ev.StartSweep(1 * time.Hour)
		log.Printf("event hostnames enabled under: %s", strings.Join(cfg.EventDomains, ", "))
	}

	// Per-endpoint API analytics for the admin page: every /v1/* request is
	// counted (route, status, caller IP + geo) into daily aggregates.
	apiMetrics := handler.NewAPIMetrics(db)
	mux.Handle("GET /v1/admin/api-analytics", authMW(http.HandlerFunc(apiMetrics.AdminSummary)))

	// Server-side visitor analytics: tail the nginx analytics log into daily
	// aggregates. Off unless ANALYTICS_LOG is set (safe default for local dev).
	if cfg.AnalyticsLog != "" {
		analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain).
			Start(5 * time.Minute)
		log.Printf("analytics ingester enabled: %s", cfg.AnalyticsLog)
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.LegacyHostRedirect(cfg.SiteDomain, cfg.ContentHost, db, handler.SecurityHeaders(handler.CORS(apiMetrics.Wrap(mux)))),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
