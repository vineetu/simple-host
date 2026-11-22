package siteshttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

func TestPausedSiteBaseRedirect(t *testing.T) {
	ed := edition.Default(config.DefaultLimits())
	h := &SiteHandler{Handler: &sitesroute.Handler{Ed: ed, SiteDomain: "simple-host.app", Hosts: sites.NewHosts(nil, nil, ed, "simple-host.app", "", "", "")}}
	h.Hosts.SetSiteHosts("off", "/app-certs")
	h.Hosts.SetSiteBase("simple-host.site", "off")
	app := h.SiteBaseHosts(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, host := range []string{"vineetu.simple-host.site", "sites.simple-host.site"} {
		for _, path := range []string{"/", "/menu/a%2Fb?from=shared", "//other.example/path?q=1"} {
			for _, method := range []string{"GET", "POST"} {
				r := httptest.NewRequest(method, "https://"+host+path, nil)
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				want := "https://" + strings.TrimSuffix(host, "simple-host.site") + "simple-host.app" + path
				if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != want {
					t.Fatalf("%s %s%s: %d %q, want 301 %q", method, host, path, w.Code, w.Header().Get("Location"), want)
				}
			}
		}
	}
	for _, host := range []string{"simple-host.app", "shop.vineetu.simple-host.app", "vineetu.simple-host.site.example.com"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "https://"+host+"/", nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: unexpectedly redirected", host)
		}
	}
	if h.HandoutBase() != "simple-host.app" || strings.Join(h.ServedBases(), " ") != "simple-host.app" {
		t.Fatal("paused base is served or handed out")
	}
}

func TestSiteBaseModes(t *testing.T) {
	a := withHosts(&SiteHandler{Handler: &sitesroute.Handler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app"}})
	for _, c := range []struct {
		base, move string
		split      bool
		handout    string
		served     string
	}{
		{"", "", false, "simple-host.app", "simple-host.app"},
		{"simple-host.site", "bogus", false, "simple-host.app", "simple-host.app"},
		{"simple-host.site", "off", false, "simple-host.app", "simple-host.app"},
		{"Simple-Host.Site.", "flat", true, "simple-host.site", "simple-host.site simple-host.app"},
		// Nested with the app's domain: refused.
		{"u.simple-host.app", "flat", false, "simple-host.app", "simple-host.app"},
	} {
		a.Hosts.SetSiteBase(c.base, c.move)
		if a.Hosts.Addressing().BaseSplit() != c.split || a.Hosts.Addressing().HandoutBase() != c.handout || strings.Join(a.Hosts.ServedBases(), " ") != c.served {
			t.Errorf("%q/%q: split %v handout %s served %v", c.base, c.move,
				a.Hosts.Addressing().BaseSplit(), a.Hosts.Addressing().HandoutBase(), a.Hosts.ServedBases())
		}
		cfg := config.Config{SiteDomain: "simple-host.app", SiteBaseDomain: strings.ToLower(strings.TrimSuffix(c.base, ".")), SiteBaseMove: c.move}
		if cfg.SiteBaseDomain == "" {
			cfg.SiteBaseDomain = cfg.SiteDomain
		}
		if got := cfg.HandoutBase(); got != c.handout {
			t.Errorf("%q/%q: config handout %s, handler %s", c.base, c.move, got, c.handout)
		}
	}
}

// TestSiteBaseNormalizeDomain: a custom domain may never be one of our zones,
// the base included even before the move is on.
func TestSiteBaseNormalizeDomain(t *testing.T) {
	h := withHosts(&SiteHandler{Handler: &sitesroute.Handler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}})
	h.Hosts.SetSiteBase("simple-host.site", "off")
	for _, d := range []string{"simple-host.site", "x.simple-host.site", "a.b.simple-host.site", "x.simple-host.app"} {
		if _, err := h.NormalizeDomain(d); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
	if _, err := h.NormalizeDomain("shop.example.com"); err != nil {
		t.Fatal(err)
	}
}
