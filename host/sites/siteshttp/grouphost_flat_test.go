package siteshttp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
)

// hackFlatApp is a bare, DB-less SiteHandler wired as a hosted-events
// instance with site hosts on, matching the hack_legacy_storage_test.go
// pattern: these functions (host parsing, TeamSiteURL, teamSitesReady) are
// pure given the handler's own fields, no database needed.
func groupFlatApp(t *testing.T, siteCertDir string) *SiteHandler {
	t.Helper()
	if siteCertDir != "" {
		for _, d := range []string{"requests", "ready"} {
			if err := os.MkdirAll(filepath.Join(siteCertDir, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	h := withHosts(&SiteHandler{Ed: groupTestEdition(), SiteDomain: "simple-hack.test"})
	h.Hosts.SetPersonHosts("canonical")
	h.Hosts.SetSiteHosts("canonical", siteCertDir)
	return h
}

func TestTeamSiteURLFlatHosts(t *testing.T) {
	h := groupFlatApp(t, "")
	if got := h.TeamSiteURL("weekend", "alpha"); got != "https://alpha.weekend.simple-hack.test/" {
		t.Errorf("off: TeamSiteURL = %q", got)
	}
	h.SetTeamSiteFlatHosts(true)
	if got := h.TeamSiteURL("weekend", "alpha"); got != "https://alpha--weekend.simple-hack.test/" {
		t.Errorf("on: TeamSiteURL = %q", got)
	}
	// Off on a Host-edition instance even if someone sets the field: the
	// setting only ever takes effect for hosted events.
	host := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.test"})
	host.Hosts.SetPersonHosts("canonical")
	host.Hosts.SetSiteHosts("canonical", "")
	host.SetTeamSiteFlatHosts(true)
	if got := host.TeamSiteURL("weekend", "alpha"); got != "https://alpha.weekend.simple-host.test/" {
		t.Errorf("Host edition: TeamSiteURL = %q, want the two-level form regardless", got)
	}
}

// TestSiteHostPartsFlatHosts covers the parts of siteHostParts that never
// need a database: off mode (no flat logic runs at all), the two-level form
// (parses the same whether flat is on or off), and malformed single labels
// that are rejected before any handle lookup would be attempted (no "--" at
// all, or an empty team/event after splitting). The happy-path flat split,
// and the exact-handle-match-wins check that guards it, need a real account
// to check against and are covered by the DB-backed tests below.
func TestSiteHostPartsFlatHosts(t *testing.T) {
	h := groupFlatApp(t, "")
	ctx := context.Background()

	site, handle, base, ok := h.Hosts.SiteHostParts(ctx, "alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("two-level: %q %q %q %v", site, handle, base, ok)
	}

	if _, _, _, ok := h.Hosts.SiteHostParts(ctx, "alpha--weekend.simple-hack.test"); ok {
		t.Fatal("off: a flat label resolved as a team site")
	}

	h.SetTeamSiteFlatHosts(true)
	// The two-level form still parses the same with flat mode on.
	site, handle, base, ok = h.Hosts.SiteHostParts(ctx, "alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("flat, two-level still parses: %q %q %q %v", site, handle, base, ok)
	}
	// Fails closed with no database to check against: a flat-shaped,
	// handle-addressable label can never be confirmed NOT a real handle, so
	// it is never read as a team site here (see the DB-backed tests for the
	// same label resolving once there is a database to check).
	if _, _, _, ok := h.Hosts.SiteHostParts(ctx, "alpha--weekend.simple-hack.test"); ok {
		t.Fatal("flat, no database: a flat label resolved without being able to rule out a real handle")
	}

	for _, host := range []string{
		"weekend.simple-hack.test",   // one label, no "--": an event host, not a team site
		"--weekend.simple-hack.test", // empty team
	} {
		if _, _, _, ok := h.Hosts.SiteHostParts(ctx, host); ok {
			t.Errorf("flat: %q wrongly resolved as a team site", host)
		}
	}
}
