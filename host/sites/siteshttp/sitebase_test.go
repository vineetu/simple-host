package siteshttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
)

func TestPausedSiteBaseRedirect(t *testing.T) {
	ed := edition.Default(config.DefaultLimits())
	h := &SiteHandler{Ed: ed, SiteDomain: "simple-host.app", Hosts: sites.NewHosts(nil, nil, ed, "simple-host.app", "", "", "")}
	h.Hosts.SetSiteHosts("off", "/app-certs")
	h.Hosts.SetSiteBase("simple-host.site", "off", "")
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
	if h.HandoutBase() != "simple-host.app" || strings.Join(h.ServedBases(), " ") != "simple-host.app" ||
		strings.Join(h.Hosts.CertRequestBases(), " ") != "simple-host.app" {
		t.Fatal("paused base is served, handed out or requests certificates")
	}
}

func TestSiteBaseModes(t *testing.T) {
	a := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app"})
	for _, c := range []struct {
		base, move      string
		split           bool
		handout         string
		served          string
		redirects, perm bool
	}{
		{"", "", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.site", "bogus", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.site", "serve", true, "simple-host.app", "simple-host.site simple-host.app", false, false},
		{"simple-host.site", "redirect", true, "simple-host.site", "simple-host.site simple-host.app", true, false},
		{"Simple-Host.Site.", "permanent", true, "simple-host.site", "simple-host.site simple-host.app", true, true},
		// Nested with the app's domain: refused.
		{"u.simple-host.app", "canonical", false, "simple-host.app", "simple-host.app", false, false},
	} {
		a.Hosts.SetSiteBase(c.base, c.move, "")
		if a.Hosts.Addressing().BaseSplit() != c.split || a.Hosts.Addressing().HandoutBase() != c.handout || strings.Join(a.Hosts.ServedBases(), " ") != c.served ||
			a.legacyRedirects() != c.redirects || (a.moveStatus() == http.StatusMovedPermanently) != c.perm {
			t.Errorf("%q/%q: split %v handout %s served %v redirects %v status %d", c.base, c.move,
				a.Hosts.Addressing().BaseSplit(), a.Hosts.Addressing().HandoutBase(), a.Hosts.ServedBases(), a.legacyRedirects(), a.moveStatus())
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

func TestTwinHost(t *testing.T) {
	a := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"})
	a.Hosts.SetSiteBase("simple-host.site", "off", "")
	if a.Hosts.TwinHost("clay.simple-host.app") != "" {
		t.Fatal("twin while off")
	}
	a.Hosts.SetSiteBase("simple-host.site", "serve", "")
	for host, want := range map[string]string{
		"clay.simple-host.app":         "clay.simple-host.site",
		"CLAY.simple-host.site.":       "clay.simple-host.app",
		"shop.olive.simple-host.app":   "shop.olive.simple-host.site",
		"shop.olive.simple-host.site":  "shop.olive.simple-host.app",
		"a.b.c.simple-host.app":        "",
		"sites.simple-host.app":        "",
		"clay.example.com":             "",
		"clay.simple-host.app.example": "",
	} {
		if got := a.Hosts.TwinHost(host); got != want {
			t.Errorf("twin(%q) = %q, want %q", host, got, want)
		}
	}
}

// TestSiteBaseNormalizeDomain: a custom domain may never be one of our zones,
// the base included even before the move is on.
func TestSiteBaseNormalizeDomain(t *testing.T) {
	h := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"})
	h.Hosts.SetSiteBase("simple-host.site", "off", "")
	for _, d := range []string{"simple-host.site", "x.simple-host.site", "a.b.simple-host.site", "x.simple-host.app"} {
		if _, err := h.normalizeDomain(d); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
	if _, err := h.normalizeDomain("shop.example.com"); err != nil {
		t.Fatal(err)
	}
}
