package server

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	accountrepo "github.com/vsriram/simple-host/host/repository/accounts"
	siterepo "github.com/vsriram/simple-host/host/repository/sites"
	storagerepo "github.com/vsriram/simple-host/host/repository/storage"
	"github.com/vsriram/simple-host/host/static"
	"github.com/vsriram/simple-host/host/storage"
)

func runStaticCommand(args []string, exts []*extension.Extension) int {
	if len(args) != 0 {
		log.Print("usage: simple-host static")
		return 2
	}
	cfg, err := config.LoadStatic()
	if err != nil {
		log.Print(err)
		return 1
	}
	disk, err := storage.OpenReadOnly(cfg.DataDir)
	if err != nil {
		log.Printf("static: open data directory: %v", err)
		return 1
	}
	maintenance, err := storage.LockMaintenanceReadOnly(cfg.DataDir)
	if err != nil {
		log.Printf("static: maintenance lock: %v", err)
		return 1
	}
	defer maintenance.Close()
	// sql.Open is deliberately lazy: neither startup nor a saved-copy request
	// waits for the database to become reachable. Never migrate or repair it.
	db, err := storagerepo.OpenServingDB(cfg.DBDSN)
	if err != nil {
		log.Print("static: invalid database configuration")
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	product := "simple-host"
	ed := edition.Default(cfg.Limits)
	if len(exts) == 1 {
		product = exts[0].Name
		ed = exts[0].Edition(cfg.Limits)
	}
	httpx.SetTrustedProxies(cfg.TrustedProxyCIDRs)
	handler.SetInstanceHosts(cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, cfg.HandoutBase())
	newPages := func() *handler.SiteHandler {
		instanceURL := ""
		if len(exts) == 1 {
			instanceURL = cfg.PublicBaseURL
		}
		h := handler.NewSiteHandler(ed, instanceURL, db, disk, cfg.SiteDomain, cfg.ContentHost, cfg.CNAMETarget, "", "", "", nil, 0, "", nil, nil)
		if len(exts) == 1 && exts[0].Assets != nil {
			assets := exts[0].Assets(h)
			h.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
		}
		h.SetPersonHosts(cfg.PersonHosts)
		h.SetSiteHosts(cfg.SiteHosts, cfg.SiteCertDir)
		h.SetSiteBase(cfg.SiteBaseDomain, cfg.SiteBaseMove, cfg.SiteBaseCertDir)
		h.SetTeamSiteFlatHosts(cfg.TeamSiteFlatHosts)
		h.SetAddressFamilies(cfg.FamilyCertDir)
		return h
	}
	loader := newPages()
	accountrepo.SetPlatformDomains(loader.HandoutBase(), loader.ServedBases()...)
	handler.SetSiteBaseText(loader.ServedBases())
	// Product routing reads are already supplied by the full table loader.
	// Static never runs the product's general Wire function (it may write).
	var groups func(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error)
	if len(exts) == 1 {
		groups = exts[0].LoadRoutingFacts
	}
	copy := static.New(static.Options{DataDir: cfg.DataDir, StateDir: cfg.StateDir, Product: product, RefreshInterval: cfg.RefreshInterval,
		Load: func(ctx context.Context) (model.SiteTable, time.Time, error) {
			return siterepo.LoadSiteTable(ctx, db, groups)
		}, Lookup: func(ctx context.Context, address model.RoutingAddress) (model.SiteTable, error) {
			if address.Group != "" && len(exts) == 1 && exts[0].LookupGroupRoutingFacts != nil {
				return exts[0].LookupGroupRoutingFacts(ctx, db, address.Group, address.GroupSite)
			}
			return loader.LookupRoutingSite(ctx, address)
		}})
	target, _ := url.Parse(cfg.APIURL)
	public, _ := url.Parse(cfg.PublicBaseURL)
	proxy := static.NewProxy(target, cfg.TrustedProxyCIDRs, loader.StaticErrorPage())
	pages := static.NewHandler(copy, proxy, func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		return newPages().StaticPages(table, api, retry)
	}, func(r *http.Request) bool {
		if !strings.HasPrefix(r.URL.Path, "/internal/") {
			host := (&url.URL{Host: r.Host}).Hostname()
			if strings.EqualFold(host, target.Hostname()) || strings.EqualFold(host, public.Hostname()) {
				return true
			}
		}
		return loader.StaticAPIRequest(r)
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tlsAsk := httpx.RateLimitByIP(httpx.NewRateLimiter(float64(cfg.Limits.RateTLSAsk.Burst), cfg.Limits.RateTLSAsk.PerSecond()), pages)
	served := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/tls-ask" {
			tlsAsk.ServeHTTP(w, r)
			return
		}
		pages.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: net.JoinHostPort(cfg.Bind, cfg.Port), Handler: served, ReadHeaderTimeout: 5 * time.Second}
	if err := static.Serve(ctx, server, copy); err != nil {
		log.Printf("static: listener: %v", err)
		return 1
	}
	return 0
}
