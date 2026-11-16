package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestHostWiring(t *testing.T) {
	cfg := servertest.Config(t, testdb.Fresh(t))
	app, err := server.Build(cfg, nil, programOptions(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	servertest.CheckWiring(t, app, "../..", "host")
}

func TestCompatibilityRedirects(t *testing.T) {
	for _, product := range []bool{false, true} {
		for _, domain := range []string{"simple-host.app", "host.example.test"} {
			t.Run(fmt.Sprintf("product=%t/%s", product, domain), func(t *testing.T) {
				cfg := servertest.Config(t, testdb.Fresh(t))
				cfg.SiteDomain = domain
				cfg.PublicBaseURL = "https://" + domain
				app, err := server.Build(cfg, programExtensions(product), programOptions(product))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(app.Close)
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					for _, path := range []string{"/hackathons", "/hackathons.html"} {
						w := httptest.NewRecorder()
						app.Handler.ServeHTTP(w, httptest.NewRequest(method, cfg.PublicBaseURL+path+"?from=legacy", nil))
						want := http.StatusFound
						if product {
							want = http.StatusOK
						}
						if w.Code != want || (!product && w.Header().Get("Location") != "/") {
							t.Errorf("%s %s: status %d, Location %q", method, path, w.Code, w.Header().Get("Location"))
						}
					}
				}
			})
		}
	}
}
