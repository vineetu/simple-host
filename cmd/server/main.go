package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	dbpkg "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
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

	handler.RegisterHealthRoutes(mux, db)
	handler.NewUserHandler(db, mailer, cfg.PublicBaseURL).Register(mux, authMW, noticeMW)
	siteHandler := handler.NewSiteHandler(db, diskStorage, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.CustomDomainIP, cfg.DeployScript, cfg.AdminAPIKey, cfg.PreviewAccounts, cfg.PreviewTTL)
	siteHandler.Register(mux, authMW, noticeMW)
	handler.RegisterTemplateRoutes(mux)
	handler.RegisterUIRoutes(mux, cfg.PublicBaseURL, siteHandler)
	handler.RegisterSkillsHub(mux, cfg.PublicBaseURL)

	// Optional "create with AI" endpoint. Sign-in-gated + rate limited; only
	// enabled when an Anthropic key is configured (it spends real credits).
	if cfg.AgentServerURL != "" || cfg.AnthropicAPIKey != "" {
		handler.NewGenerateHandler(cfg.AnthropicAPIKey, cfg.GenerateModel, cfg.AgentServerURL, cfg.AgentSharedSecret).Register(mux, authMW)
		if cfg.AgentServerURL != "" {
			log.Printf("AI create endpoint enabled (/v1/generate -> agent server %s)", cfg.AgentServerURL)
		} else {
			log.Printf("AI create endpoint enabled (/v1/generate, direct Messages API, model %s)", cfg.GenerateModel)
		}
	} else {
		log.Printf("neither AGENT_SERVER_URL nor ANTHROPIC_API_KEY set; /v1/generate (AI create) disabled")
	}

	// Server-side visitor analytics: tail the nginx analytics log into daily
	// aggregates. Off unless ANALYTICS_LOG is set (safe default for local dev).
	if cfg.AnalyticsLog != "" {
		analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain).
			Start(5 * time.Minute)
		log.Printf("analytics ingester enabled: %s", cfg.AnalyticsLog)
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.LegacyHostRedirect(cfg.SiteDomain, cfg.ContentHost, db, handler.SecurityHeaders(handler.CORS(mux))),
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
