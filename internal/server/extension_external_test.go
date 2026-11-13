package server_test

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestExtensionConstructionOrderAndWireError(t *testing.T) {
	cfg := servertest.Config(t, testdb.Fresh(t))
	cfg.PhotoDNAKey = "test-only-no-provider-requests"
	var order []string
	var db *sql.DB
	wantErr := errors.New("test wiring failure")
	ext := &extension.Extension{
		Name: "test",
		Edition: func(limits config.Limits) *edition.Edition {
			order = append(order, "edition")
			return edition.ForKindWithLimits(edition.Host, limits)
		},
		Early: func(d extension.EarlyDeps) extension.EarlyHooks {
			order = append(order, "early")
			db = d.DB
			if d.Edition == nil || d.Config.DataDir != cfg.DataDir {
				t.Fatal("early dependencies missing")
			}
			return extension.EarlyHooks{}
		},
		Gate: func(d extension.GateDeps) func(http.Handler) http.Handler {
			order = append(order, "gate")
			if d.Site == nil || d.Edition == nil {
				t.Fatal("gate dependencies missing")
			}
			return func(next http.Handler) http.Handler { return next }
		},
		Wire: func(d extension.Deps) (extension.Parts, error) {
			order = append(order, "wire")
			if d.DB != db || d.Disk == nil || d.Mail == nil || d.Accounts == nil || d.Site == nil || d.Connector == nil || d.ImageScan == nil || d.AuthMW == nil || d.Mux == nil || d.Edition == nil {
				t.Fatal("wire dependencies missing")
			}
			_, pattern := d.Mux.Handler(httptest.NewRequest(http.MethodPost, "/mcp", nil))
			if pattern != "POST /mcp" {
				t.Fatalf("connector not registered before Wire: %q", pattern)
			}
			return extension.Parts{}, wantErr
		},
	}
	app, err := server.Build(cfg, []*extension.Extension{ext})
	if app != nil || !errors.Is(err, wantErr) {
		t.Fatalf("Build = %v, %v; want wiring failure", app, err)
	}
	if !reflect.DeepEqual(order, []string{"edition", "early", "gate", "wire"}) {
		t.Fatalf("construction order = %v", order)
	}
	if err := db.Ping(); !errors.Is(err, sql.ErrConnDone) && (err == nil || err.Error() != "sql: database is closed") {
		t.Fatalf("database not closed after Wire failure: %v", err)
	}
}
