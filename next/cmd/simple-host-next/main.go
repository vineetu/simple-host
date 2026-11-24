// Command simple-host-next is the Simple Host service: it reads its config
// from the environment, opens the database (creating the tables on an empty
// one), mounts every feature, starts the workers and serves.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"simplehost/access/connector"
	"simplehost/access/keys"
	"simplehost/access/signin"
	"simplehost/access/teams"
	"simplehost/access/visitors"
	"simplehost/app"
	"simplehost/sites/domains"
	"simplehost/sites/locks"
	"simplehost/sites/manage"
	"simplehost/sites/publicpage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/stats"
	"simplehost/sites/uploads"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg := app.FromEnv()
	a, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	mount(a)
	work(ctx, a)
	srv := &http.Server{Addr: cfg.Addr, Handler: a, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("simple-host-next listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// mount adds every feature's routes: one line per feature, Mount(a).
// The journey tests mount the same list through the test kit.
func mount(a *app.App) {
	signin.Mount(a)
	keys.Mount(a)
	teams.Mount(a)
	connector.Mount(a)
	visitors.Mount(a)
	domains.Mount(a)
	locks.Mount(a)
	publish.Mount(a)
	uploads.Mount(a)
	manage.Mount(a)
	serve.Mount(a)
	stats.Mount(a)
	publicpage.Mount(a)
}

// work starts the background workers: one line per worker, go Work(ctx, a).
func work(ctx context.Context, a *app.App) {
	go domains.Work(ctx, a)
	go serve.Work(ctx, a)
	go manage.Work(ctx, a)
}
