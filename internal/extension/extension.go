// Package extension describes a product's contributions to the server.
// Hooks are added here only as their consumers are extracted from the platform.
package extension

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/vsriram/simple-host/db/migrations"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/workers"
)

type Extension struct {
	Name    string
	Edition func(config.Limits) *edition.Edition
	// Migrations and MigrationOrder supply the product catalogue and the frozen
	// combined order of platform and product unnumbered migrations.
	Migrations     *migrations.Catalog
	MigrationOrder []string
	// Gate runs once before connector construction. Its middleware sits inside
	// LegacyStorageCutoffGate and outside ScopeGate.
	Gate func(GateDeps) func(http.Handler) http.Handler
	// Wire runs once after connector registration and image-scan construction.
	Wire func(Deps) (Parts, error)
	// Early runs after edition construction, before the first accounts service.
	Early func(EarlyDeps) EarlyHooks
}

type EarlyDeps struct {
	DB      *sql.DB
	Config  config.Config
	Edition *edition.Edition
}

type EarlyHooks struct{}

type GateDeps struct {
	Site    *handler.SiteHandler
	Edition *edition.Edition
}

type Deps struct {
	DB        *sql.DB
	Disk      *storage.DiskStorage
	Mail      *email.ResendSender
	Accounts  *accountapp.Service
	Site      *handler.SiteHandler
	Connector *handler.ConnectorHandler
	ImageScan *operations.ImageScan
	AuthMW    func(http.Handler) http.Handler
	Mux       *http.ServeMux
	Config    config.Config
	Edition   *edition.Edition
}

type Parts struct {
	Workers       []workers.Owner
	SiteSweep     func(context.Context)
	PaperHack     func(*papermetrics.Collector)
	PaperRecorder func(http.Handler) http.Handler
	Hooks         Hooks
}

type Hooks struct {
	Sitemap sites.SitemapFilter
}
