package server_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestExtensionConstructionOrderAndWireError(t *testing.T) {
	cfg := servertest.Config(t, testdb.Fresh(t))
	var order []string
	var db *sql.DB
	wantErr := errors.New("test wiring failure")
	ext := &extension.Extension{
		Name: "test",
		CheckSchema: func(ctx context.Context, database *sql.DB) error {
			order = append(order, "schema")
			db = database
			return nil
		},
		Edition: func(limits config.Limits) *edition.Edition {
			order = append(order, "edition")
			return edition.Default(limits)
		},
		Early: func(d extension.EarlyDeps) extension.EarlyHooks {
			order = append(order, "early")
			if d.DB != db || d.Edition == nil || d.Config.DataDir != cfg.DataDir {
				t.Fatal("early dependencies missing")
			}
			return extension.EarlyHooks{}
		},
		Wire: func(d extension.Deps) (extension.Parts, error) {
			order = append(order, "wire")
			if d.DB != db || d.Disk == nil || d.Mail == nil || d.Accounts == nil || d.Site == nil || d.Connector == nil || d.AuthMW == nil || d.Mux == nil || d.Edition == nil {
				t.Fatal("wire dependencies missing")
			}
			_, pattern := d.Mux.Handler(httptest.NewRequest(http.MethodPost, "/mcp", nil))
			if pattern != "POST /mcp" {
				t.Fatalf("connector not registered before Wire: %q", pattern)
			}
			return extension.Parts{}, wantErr
		},
	}
	servertest.RecordProduct(t, cfg, []*extension.Extension{ext})
	app, err := server.Build(cfg, []*extension.Extension{ext})
	if app != nil || !errors.Is(err, wantErr) {
		t.Fatalf("Build = %v, %v; want wiring failure", app, err)
	}
	if !reflect.DeepEqual(order, []string{"schema", "edition", "early", "wire"}) {
		t.Fatalf("construction order = %v", order)
	}
	if err := db.Ping(); !errors.Is(err, sql.ErrConnDone) && (err == nil || err.Error() != "sql: database is closed") {
		t.Fatalf("database not closed after Wire failure: %v", err)
	}
}

func TestExtensionSchemaCheckFailure(t *testing.T) {
	for _, missingPlatformColumn := range []bool{false, true} {
		t.Run(fmt.Sprint(missingPlatformColumn), func(t *testing.T) {
			cfg := servertest.Config(t, testdb.Fresh(t))
			if missingPlatformColumn {
				db, err := sql.Open("postgres", cfg.DBDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec(`ALTER TABLE account_passwords DROP COLUMN login`); err != nil {
					t.Fatal(err)
				}
			}
			wantErr := errors.New("product schema missing")
			called := false
			ext := &extension.Extension{
				CheckSchema: func(context.Context, *sql.DB) error {
					called = true
					return wantErr
				},
				Early: func(extension.EarlyDeps) extension.EarlyHooks {
					t.Fatal("early database consumers ran after schema failure")
					return extension.EarlyHooks{}
				},
			}
			servertest.RecordProduct(t, cfg, []*extension.Extension{ext})
			app, err := server.Build(cfg, []*extension.Extension{ext})
			if app != nil || err == nil {
				t.Fatalf("Build = %v, %v; want schema failure", app, err)
			}
			if missingPlatformColumn {
				if called || !strings.Contains(err.Error(), "account_passwords.login") {
					t.Fatalf("platform check must fail before product check: called=%v, err=%v", called, err)
				}
			} else if !called || !errors.Is(err, wantErr) {
				t.Fatalf("product failure not returned: called=%v, err=%v", called, err)
			}
		})
	}
}
