package handler

import (
	"net/http"
	"os"
	"regexp"
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
	if validateHandle("setup") == nil {
		t.Error("the handle \"setup\" must be reserved")
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

// The helper's install command fetches the installer from the release the
// installer pins, so the two must name the same tag: bumping VERSION in
// install.sh without setup.js would hand out an older installer.
func TestSetupHelperInstallerRelease(t *testing.T) {
	js, err := staticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := os.ReadFile("../../deploy/install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	page := regexp.MustCompile(`var INSTALLER_RELEASE = '(v[0-9.]+)';`).FindSubmatch(js)
	pinned := regexp.MustCompile(`(?m)^VERSION="(v[0-9.]+)"$`).FindSubmatch(sh)
	if page == nil || pinned == nil {
		t.Fatalf("release not found: setup.js %q, install.sh %q", page, pinned)
	}
	if string(page[1]) != string(pinned[1]) {
		t.Errorf("setup.js INSTALLER_RELEASE = %s, install.sh pins %s", page[1], pinned[1])
	}
	if !strings.Contains(string(js), "'https://raw.githubusercontent.com/vineetu/simple-host/' + INSTALLER_RELEASE + '/deploy/install/install.sh'") {
		t.Error("the install command must fetch install.sh from the pinned release, not a branch")
	}
	// The UpCloud button is the referral link, exactly, opened apart from
	// this page.
	if !strings.Contains(string(js), "var UPCLOUD_SIGNUP = 'https://signup.upcloud.com/?promo=JF2WCV';") {
		t.Error("UPCLOUD_SIGNUP changed")
	}
}
