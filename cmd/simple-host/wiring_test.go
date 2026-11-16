package main

import (
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
	app, err := server.Build(cfg, nil, programOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	servertest.CheckWiring(t, app, "../..", "internal/server/testdata", "host")
}

func TestCompatibilityRedirects(t *testing.T) {
	for _, domain := range []string{"simple-host.app", "host.example.test"} {
		t.Run(domain, func(t *testing.T) {
			cfg := servertest.Config(t, testdb.Fresh(t))
			cfg.SiteDomain = domain
			cfg.PublicBaseURL = "https://" + domain
			app, err := server.Build(cfg, nil, programOptions())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(app.Close)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, path := range []string{"/hackathons", "/hackathons.html"} {
					w := httptest.NewRecorder()
					app.Handler.ServeHTTP(w, httptest.NewRequest(method, cfg.PublicBaseURL+path+"?from=legacy", nil))
					if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
						t.Errorf("%s %s: status %d, Location %q", method, path, w.Code, w.Header().Get("Location"))
					}
				}
			}
		})
	}
}
