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

	"simplehost/app"
	"simplehost/data/files"
	"simplehost/data/recipes"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
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
	rows.Mount(a)
	values.Mount(a)
	files.Mount(a)
	datasql.Mount(a)
	datarules.Mount(a)
	recipes.Mount(a)
}

// work starts the background workers: one line per worker, go Work(ctx, a).
func work(ctx context.Context, a *app.App) {
}
