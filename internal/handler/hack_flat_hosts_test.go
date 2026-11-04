package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

// hackFlatApp is a bare, DB-less SiteHandler wired as a hosted-events
// instance with site hosts on, matching the hack_legacy_storage_test.go
// pattern: these functions (host parsing, TeamSiteURL, teamSitesReady) are
// pure given the handler's own fields, no database needed.
func hackFlatApp(t *testing.T, siteCertDir string) *SiteHandler {
	t.Helper()
	if siteCertDir != "" {
		for _, d := range []string{"requests", "ready"} {
			if err := os.MkdirAll(filepath.Join(siteCertDir, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	h := &SiteHandler{ed: edition.ForKind(edition.Hack), siteDomain: "simple-hack.test"}
	h.SetPersonHosts("canonical")
	h.SetSiteHosts("canonical", siteCertDir)
	return h
}

func TestTeamSiteURLFlatHosts(t *testing.T) {
	h := hackFlatApp(t, "")
	if got := h.TeamSiteURL("weekend", "alpha"); got != "https://alpha.weekend.simple-hack.test/" {
		t.Errorf("off: TeamSiteURL = %q", got)
	}
	h.SetTeamSiteFlatHosts(true)
	if got := h.TeamSiteURL("weekend", "alpha"); got != "https://alpha--weekend.simple-hack.test/" {
		t.Errorf("on: TeamSiteURL = %q", got)
	}
	// Off on a Host-edition instance even if someone sets the field: the
	// setting only ever takes effect for hosted events.
	host := &SiteHandler{ed: edition.ForKind(edition.Host), siteDomain: "simple-host.test"}
	host.SetPersonHosts("canonical")
	host.SetSiteHosts("canonical", "")
	host.SetTeamSiteFlatHosts(true)
	if got := host.TeamSiteURL("weekend", "alpha"); got != "https://alpha.weekend.simple-host.test/" {
		t.Errorf("Host edition: TeamSiteURL = %q, want the two-level form regardless", got)
	}
}

// TestTeamSitesReadyDoesNotWaitOnPerEventCert: with a certificate hand-off
// directory configured but no per-event "ready" marker ever written, flat
// mode must still report ready (the instance's own *.<base> wildcard is
// assumed); off mode must not.
func TestTeamSitesReadyDoesNotWaitOnPerEventCert(t *testing.T) {
	dir := t.TempDir()
	h := hackFlatApp(t, dir)
	if h.teamSitesReady("weekend") {
		t.Fatal("off: ready without a certificate marker")
	}
	h.SetTeamSiteFlatHosts(true)
	if !h.teamSitesReady("weekend") {
		t.Fatal("flat: not ready despite the instance wildcard being assumed")
	}
	if h.teamSitesReady("") {
		t.Fatal("flat: ready for an empty event slug")
	}
}

func TestRequestSiteCertSkipsPerEventUnderFlatHosts(t *testing.T) {
	dir := t.TempDir()
	h := hackFlatApp(t, dir)
	h.RequestSiteCert("weekend")
	if _, err := os.Stat(filepath.Join(dir, "requests", "weekend")); err != nil {
		t.Fatalf("off: expected a certificate request file: %v", err)
	}
	h.SetTeamSiteFlatHosts(true)
	h.RequestSiteCert("another-event")
	if _, err := os.Stat(filepath.Join(dir, "requests", "another-event")); err == nil {
		t.Fatal("flat: a per-event certificate was requested; it should never be")
	}
}

// TestSiteHostPartsFlatHosts: a single <team>--<event> label resolves to the
// same (site, handle) pair as the two-level form, only when flat mode is on;
// off, it resolves to nothing (never read as a team site), and a plain
// two-level host is unaffected either way.
func TestSiteHostPartsFlatHosts(t *testing.T) {
	h := hackFlatApp(t, "")

	site, handle, base, ok := h.siteHostParts("alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("two-level: %q %q %q %v", site, handle, base, ok)
	}

	if _, _, _, ok := h.siteHostParts("alpha--weekend.simple-hack.test"); ok {
		t.Fatal("off: a flat label resolved as a team site")
	}

	h.SetTeamSiteFlatHosts(true)
	site, handle, base, ok = h.siteHostParts("alpha--weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("flat: %q %q %q %v", site, handle, base, ok)
	}
	// The two-level form still parses too (sitehost.go's own TLS/cert
	// readiness is what actually makes it unreachable in production; see
	// teamSitesReady).
	site, handle, base, ok = h.siteHostParts("alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("flat, two-level still parses: %q %q %q %v", site, handle, base, ok)
	}

	for _, host := range []string{
		"weekend.simple-hack.test",               // one label, no "--": an event host, not a team site
		"alpha--weekend--extra.simple-hack.test", // ambiguous, more than one "--"
		"--weekend.simple-hack.test",             // empty team
		"alpha--.simple-hack.test",               // empty event
	} {
		if _, _, _, ok := h.siteHostParts(host); ok {
			t.Errorf("flat: %q wrongly resolved as a team site", host)
		}
	}
}
