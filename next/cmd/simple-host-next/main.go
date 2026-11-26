// Command simple-host-next is the Simple Host service: it reads its config
// from the environment, opens the database (creating the tables on an empty
// one), mounts every feature, starts the workers and serves.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"simplehost/access/connector"
	"simplehost/access/keys"
	"simplehost/access/signin"
	"simplehost/access/teams"
	"simplehost/access/visitors"
	"simplehost/api"
	"simplehost/app"
	"simplehost/compat"
	"simplehost/data/files"
	"simplehost/data/recipes"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
	"simplehost/mcp"
	"simplehost/ops/admin"
	"simplehost/ops/erase"
	"simplehost/ops/export"
	"simplehost/ops/holds"
	"simplehost/ops/idle"
	"simplehost/ops/notify"
	"simplehost/sites/domains"
	"simplehost/sites/locks"
	"simplehost/sites/manage"
	"simplehost/sites/publicpage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/stats"
	"simplehost/sites/uploads"
	"simplehost/web"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "review-hash" {
		reviewHash()
		return
	}
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
	srv := &http.Server{Addr: cfg.Addr, Handler: compat.Wrap(a, admin.Counted(a)), ReadHeaderTimeout: 10 * time.Second}
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
	rows.Mount(a)
	values.Mount(a)
	files.Mount(a)
	datasql.Mount(a)
	datarules.Mount(a)
	recipes.Mount(a)
	api.Mount(a)
	mcp.Mount(a)
	notify.Mount(a)
	export.Mount(a)
	erase.Mount(a)
	admin.Mount(a)
	holds.Mount(a)
	web.Mount(a)
}

// work starts the background workers: one line per worker, go Work(ctx, a).
func work(ctx context.Context, a *app.App) {
	go domains.Work(ctx, a)
	go serve.Work(ctx, a)
	go manage.Work(ctx, a)
	go notify.Work(ctx, a)
	go export.Work(ctx, a)
	go idle.Work(ctx, a)
	go admin.Work(ctx, a)
}

// reviewHash reads a password (one line) and prints SH_REVIEW_PASSWORD_HASH:
//
//	read -rs PW && printf '%s\n' "$PW" | simple-host-next review-hash; unset PW
func reviewHash() {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	hash, err := signin.HashReviewPassword(strings.TrimRight(line, "\r\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(hash)
}
