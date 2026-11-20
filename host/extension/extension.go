// Package extension describes a product's contributions to the server.
// Hooks are added here only as their consumers are extracted from the platform.
package extension

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/app/sites"
	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/workers"
)

// ConfigSpec contributes product setting metadata and startup parsing.
type ConfigSpec struct {
	Settings []config.Setting
	Load     func(getenv func(string) string) (warnings []string, err error)
}
type Extension struct {
	Config ConfigSpec

	Assets func(*handler.SiteHandler) Assets
	// CheckSchema runs after platform verification, before other database reads.
	// It also runs after migration verification; nil adds no product checks.
	CheckSchema func(context.Context, *sql.DB) error
	Name        string
	// ValidateProduct checks product evidence before adopting an existing database.
	ValidateProduct func(context.Context, *sql.Conn) error
	Edition         func(config.Limits) *edition.Edition
	// Migrations and MigrationOrder supply the product catalogue and the frozen
	// combined order of platform and product unnumbered migrations.
	Migrations     *migrations.Catalog
	MigrationOrder []string
	// Wire runs once after connector registration.
	Wire func(Deps) (Parts, error)
	// Early runs after edition construction, before the first accounts service.
	Early func(EarlyDeps) EarlyHooks
}

type EarlyDeps struct {
	DB      *sql.DB
	Config  config.Config
	Edition *edition.Edition
}

type EarlyHooks struct {
	Accounts accountapp.GroupHooks
}

type Deps struct {
	DB        *sql.DB
	Disk      handler.SiteDisk
	Mail      *email.ResendSender
	Accounts  *accountapp.Service
	Site      *handler.SiteHandler
	Connector *handler.ConnectorHandler
	AuthMW    func(http.Handler) http.Handler
	Mux       *http.ServeMux
	Config    config.Config
	Edition   *edition.Edition
}

type Parts struct {
	Workers        []workers.Owner
	SiteSweep      handler.GroupSweep
	ProductMetrics func(*papermetrics.Collector)
	PaperRecorder  func(http.Handler) http.Handler
	// Tools is the product's AI tools; the zero value adds none.
	Tools mcp.Contribution
	Hooks Hooks
}

type Hooks struct {
	Storage    appstorage.BindingProvider
	Sitemap    sites.SitemapFilter
	GroupHosts handler.GroupHosts
	GroupGate  sites.GroupGate
}

// Assets describes the product's file contribution. Replace is authoritative:
// a missing replacement never falls back to a platform file. Files outside
// Replace are exposed only by Routes.
type Assets struct {
	// Replace: platform asset name -> file in Files. Only platform names.
	Replace map[string]string
	// Own: the product's own file names -> file in Files. The platform's page
	// pipeline can read them (a product page it renders), and its file
	// server never answers them: over HTTP each is reachable only where
	// Routes mounted handler.AssetServices.ProductFiles.
	Own     map[string]string
	Files   fs.FS
	Skills  fs.FS
	Routes  func(*http.ServeMux, handler.AssetServices)
	Hooks   handler.ProductAssetHooks
	Rewrite func(string, []byte) []byte
}
