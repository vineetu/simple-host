package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/geoip"
)

// Run is the whole program. Failures return a code without exiting the process.
func Run(args []string) int {
	if len(args) > 0 {
		for _, cmd := range commands {
			if args[0] == cmd.name {
				return cmd.run(args[1:])
			}
		}
	}
	log.Printf("%s", buildinfo.String())
	cfg, err := config.Load()
	if err != nil {
		log.Printf("load config: %v", err)
		return 1
	}
	app, err := Build(cfg)
	if err != nil {
		log.Print(err)
		return 1
	}
	defer app.Close()
	server := &http.Server{Addr: net.JoinHostPort(cfg.BindAddr, cfg.Port), Handler: app.Handler}
	if app.setupDone != nil {
		return runSetup(server, app.setupDone)
	}
	server.ReadHeaderTimeout = 5 * time.Second
	workerSet := app.Workers
	exitCode := 0
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// The signal drains HTTP before it is allowed to cancel background work.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	// Startup must finish its initial cache/retention work before serving,
	// but a signal must still interrupt a blocked initial database query.
	startupDone := make(chan struct{})
	startupWatcherDone := make(chan struct{})
	go func() {
		defer close(startupWatcherDone)
		select {
		case <-ctx.Done():
			cancelWorkers()
		case <-startupDone:
		}
	}()
	workerSet.Start(workerCtx)
	close(startupDone)
	<-startupWatcherDone

	serverErr := make(chan error, 1)

	if ctx.Err() == nil {
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
				exitCode = 1
				log.Printf("server error: %v", err)
			}
		case <-ctx.Done():
			stop()
		}

	} else {
		log.Print("startup interrupted by signal; stopping background workers")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		exitCode = 1
		log.Printf("graceful shutdown failed: %v", err)
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close failed: %v", closeErr)
		}
	}
	workerSet.Stop()
	cancelWorkers()
	// Allow an in-flight API flush (15s) followed by its final flush (15s),
	// and the paper collectors' final flushes (20s), before closing the DB.
	// HTTP drain (10s) plus this join (45s) stays below systemd's 90s stop limit.
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer joinCancel()
	laggards := workerSet.Wait(joinCtx)
	for _, name := range laggards {
		exitCode = 1
		log.Printf("worker %s: shutdown deadline exceeded", name)
	}
	if len(laggards) == 0 {
		log.Print("background workers stopped")
	}
	return exitCode
}

func runSetup(server *http.Server, done <-chan struct{}) int {
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("setup server: %v", err)
			return 1
		}
	case <-done:
		// Setup wrote its answers to the database. This process cannot use
		// them: it registered the setup page and nothing else, and the
		// hostnames and per-site cap are read at startup. So it stops, and
		// the supervisor starts it again into the configured instance.
		// Without this the organiser is told "Done", reloads, and is handed
		// the setup page for a second time.
		log.Printf("setup complete; restarting into the configured instance")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("setup server shutdown: %v", err)
		}
	}
	return 0
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
