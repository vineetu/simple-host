package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// The setup helper is served at /setup with its script, and the small-box
// settings list it reads is the one the code describes.
func TestSetupHelper(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/setup")
	if rec.Code != http.StatusOK {
		t.Fatalf("/setup: status %d", rec.Code)
	}
	body := rec.Body.String()
	assertChrome(t, "/setup", body, "")
	if !strings.Contains(body, `<script src="/setup/setup.js"></script>`) {
		t.Error("/setup does not load its script")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("/setup CSP = %q", csp)
	}
	if rec := get(t, mux, "simple-host.app", "/setup/"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/setup" {
		t.Errorf("/setup/: status %d to %q, want 301 to /setup", rec.Code, rec.Header().Get("Location"))
	}
	want, err := config.SettingsJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, mux, "simple-host.app", "/setup/small-box-settings.json").Body.String(); got != string(want) {
		t.Error("static/setup/small-box-settings.json differs from the settings in code: run scripts/sync-settings.sh")
	}
	for _, p := range []string{"/setup/setup.js", "/setup/enterprise-settings.json"} {
		if rec := get(t, mux, "simple-host.app", p); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rec.Code)
		}
	}
}
