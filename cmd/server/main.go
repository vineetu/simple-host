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

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
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

	authMW := auth.Middleware(cfg.AdminAPIKey, db)

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
	handler.NewSiteHandler(db, diskStorage, cfg.SiteDomain, cfg.DeployScript).Register(mux, authMW, noticeMW)
	handler.RegisterUIRoutes(mux)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
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
