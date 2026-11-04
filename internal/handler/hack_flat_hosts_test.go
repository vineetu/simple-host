package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/model"
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
	// siteHostFor/SiteURL/siteHostOn are the same single construction
	// (issue 3): confirm they agree with TeamSiteURL under flat mode.
	if got := h.siteHostFor("weekend", "alpha"); got != "alpha--weekend.simple-hack.test" {
		t.Errorf("on: siteHostFor = %q", got)
	}
	if got := h.SiteURL("weekend", "alpha"); got != "https://alpha--weekend.simple-hack.test/" {
		t.Errorf("on: SiteURL = %q", got)
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
// assumed) — and so must the preview-link gates, which share the same
// siteCertReady check.
func TestTeamSitesReadyDoesNotWaitOnPerEventCert(t *testing.T) {
	dir := t.TempDir()
	h := hackFlatApp(t, dir)
	if h.teamSitesReady("weekend") {
		t.Fatal("off: ready without a certificate marker")
	}
	// TeamPreviewLinkFor's certificate gate runs before it touches site
	// fields, so a zero model.Site is enough to exercise it here.
	if _, ok := h.TeamPreviewLinkFor(model.Site{}, "weekend", 1); ok {
		t.Fatal("off: TeamPreviewLinkFor ready without a certificate marker")
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

// TestSiteHostPartsFlatHosts covers the parts of siteHostParts that never
// need a database: off mode (no flat logic runs at all), the two-level form
// (parses the same whether flat is on or off), and malformed single labels
// that are rejected before any handle lookup would be attempted (no "--" at
// all, or an empty team/event after splitting). The happy-path flat split,
// and the exact-handle-match-wins check that guards it, need a real account
// to check against and are covered by the DB-backed tests below.
func TestSiteHostPartsFlatHosts(t *testing.T) {
	h := hackFlatApp(t, "")
	ctx := context.Background()

	site, handle, base, ok := h.siteHostParts(ctx, "alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("two-level: %q %q %q %v", site, handle, base, ok)
	}

	if _, _, _, ok := h.siteHostParts(ctx, "alpha--weekend.simple-hack.test"); ok {
		t.Fatal("off: a flat label resolved as a team site")
	}

	h.SetTeamSiteFlatHosts(true)
	// The two-level form still parses the same with flat mode on.
	site, handle, base, ok = h.siteHostParts(ctx, "alpha.weekend.simple-hack.test")
	if !ok || site != "alpha" || handle != "weekend" || base != "simple-hack.test" {
		t.Fatalf("flat, two-level still parses: %q %q %q %v", site, handle, base, ok)
	}
	// Fails closed with no database to check against: a flat-shaped,
	// handle-addressable label can never be confirmed NOT a real handle, so
	// it is never read as a team site here (see the DB-backed tests for the
	// same label resolving once there is a database to check).
	if _, _, _, ok := h.siteHostParts(ctx, "alpha--weekend.simple-hack.test"); ok {
		t.Fatal("flat, no database: a flat label resolved without being able to rule out a real handle")
	}

	for _, host := range []string{
		"weekend.simple-hack.test",   // one label, no "--": an event host, not a team site
		"--weekend.simple-hack.test", // empty team
		"alpha--.simple-hack.test",   // empty event
	} {
		if _, _, _, ok := h.siteHostParts(ctx, host); ok {
			t.Errorf("flat: %q wrongly resolved as a team site", host)
		}
	}
}

// --- DB-backed routing tests -------------------------------------------
//
// These build the fixture under Host edition (so the generic deploy helper
// is unrestricted), then flip the handler's edition to Hack and turn flat
// hosts on: siteHostParts/SiteHosts/siteCertReadyOn read h.ed and the flag
// live, so this exactly simulates a hosted-events instance with
// TEAM_SITE_FLAT_HOSTS on, without needing full event/team machinery.

func asHackFlat(a *privateApp) {
	a.sites.ed = edition.ForKind(edition.Hack)
	a.sites.SetTeamSiteFlatHosts(true)
}

func uniqueLabel(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func setHandle(t *testing.T, a *privateApp, p person, handle string) {
	t.Helper()
	id, _ := a.userID(t, p)
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, id); err != nil {
		t.Fatal(err)
	}
}

// makeEventAccount gives p's account a minimal, real events row (built-in
// website mode, so serveHackEventHost reaches the hackEventPage callback
// below once GetEventByAccount resolves it), and wires a stub event page so
// a request that correctly falls through to the person/event host actually
// gets distinctive, checkable content back instead of a 404.
func makeEventAccount(t *testing.T, a *privateApp, p person) {
	t.Helper()
	id, _ := a.userID(t, p)
	slug := uniqueLabel(t, "ev")
	if _, err := a.database.Exec(
		`INSERT INTO events (slug, account_id, title, organiser_name, contact_email, join_code, judge_code)
		 VALUES ($1, $2, 'Legacy Event', 'Legacy Organiser', 'legacy@example.com', $3, $4)`,
		slug, id, uniqueLabel(t, "join"), uniqueLabel(t, "judge"),
	); err != nil {
		t.Fatal(err)
	}
	a.sites.SetHackEventPage(func(w http.ResponseWriter, r *http.Request, user model.User) bool {
		if user.ID != id {
			return false
		}
		w.Write([]byte("<h1>legacy event page</h1>"))
		return true
	})
}

// TestSiteHostsFlatServesWithoutPerEventCertMarker (issue 1): with a
// certificate hand-off directory configured and no per-event "ready"
// marker ever written, the flat host must still serve the team's site —
// not 404 forever.
func TestSiteHostsFlatServesWithoutPerEventCertMarker(t *testing.T) {
	// newSiteApp already configures a certificate hand-off directory
	// (mode "canonical"); deliberately never calling markReady on it is
	// the point — the per-event marker is never written under flat mode
	// (RequestSiteCert no-ops), so this instance has none for eventHandle.
	a, _ := newSiteApp(t, "canonical")
	event := a.newPerson(t, "ev")
	eventHandle := uniqueLabel(t, "weekend")
	setHandle(t, a, event, eventHandle)
	a.deploy(t, event, "alpha")

	asHackFlat(a)

	host := "alpha--" + eventHandle + "." + pcSiteDomain
	r := a.at(t, "GET", host, "/", nil, nil)
	if r.status != 200 || !strings.Contains(string(r.body), "<h1>alpha</h1>") {
		t.Fatalf("flat host %s: %d %s", host, r.status, r.body)
	}
}

// TestSiteHostsFlatModeExactHandleWins (issue 2): a pre-existing account
// whose handle literally contains "--" (an event slug created before "--"
// was forbidden) keeps its own origin, even when splitting its label on
// "--" would otherwise resolve to a real team's site under a different
// event.
func TestSiteHostsFlatModeExactHandleWins(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	otherEvent := a.newPerson(t, "ev")
	legacyEvent := a.newPerson(t, "legacy")
	otherHandle := uniqueLabel(t, "summer")
	setHandle(t, a, otherEvent, otherHandle)
	// The dangerous collision: a real site named "red" under the OTHER
	// event's handle, matching exactly what splitting the legacy handle on
	// "--" would produce. "blue" is a second, uninvolved site used below to
	// show the flat split still works for a label that is not a real handle.
	a.deploy(t, otherEvent, "red")
	a.deploy(t, otherEvent, "blue")
	legacyHandle := "red--" + otherHandle
	setHandle(t, a, legacyEvent, legacyHandle)
	// legacyEvent is a real hosted event (not just a bare account), so its
	// own host can actually be checked to serve the event, not merely to
	// not serve "red".
	makeEventAccount(t, a, legacyEvent)

	asHackFlat(a)
	ctx := context.Background()

	if _, _, _, ok := a.sites.siteHostParts(ctx, legacyHandle+"."+pcSiteDomain); ok {
		t.Fatalf("siteHostParts resolved %q as a flat team label despite it being a real handle", legacyHandle)
	}

	host := legacyHandle + "." + pcSiteDomain
	r := a.at(t, "GET", host, "/", nil, nil)
	if strings.Contains(string(r.body), "<h1>red</h1>") {
		t.Fatalf("existing handle %q was hijacked into serving another account's %q site: %d %s", legacyHandle, "red", r.status, r.body)
	}
	// Not just "didn't serve red": it must actually serve the legacy
	// event's own page.
	if r.status != 200 || !strings.Contains(string(r.body), "<h1>legacy event page</h1>") {
		t.Fatalf("legacy event host %s did not serve the event: %d %s", host, r.status, r.body)
	}

	// Contrast: a genuine flat label (not a real handle) still splits and
	// resolves to the real team site.
	genuineHost := "blue--" + otherHandle + "." + pcSiteDomain
	r2 := a.at(t, "GET", genuineHost, "/", nil, nil)
	if r2.status != 200 || !strings.Contains(string(r2.body), "<h1>blue</h1>") {
		t.Fatalf("genuine flat host %s: %d %s", genuineHost, r2.status, r2.body)
	}

	// The two-level form for the same team is unaffected by flat mode.
	twoLevelHost := "red." + otherHandle + "." + pcSiteDomain
	r3 := a.at(t, "GET", twoLevelHost, "/", nil, nil)
	if r3.status != 200 || !strings.Contains(string(r3.body), "<h1>red</h1>") {
		t.Fatalf("two-level host %s under flat mode: %d %s", twoLevelHost, r3.status, r3.body)
	}
}

// TestSiteHostPartsFlatHostsLegacyEventSlugWithDoubleHyphen (issue 2): a
// legacy event whose own slug/handle contains "--" (predating the rule that
// forbids it) still gets correctly split at the FIRST "--": the team part
// never contains "--" by construction, so everything after it, "--" and
// all, is the event.
func TestSiteHostPartsFlatHostsLegacyEventSlugWithDoubleHyphen(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	legacyEvent := a.newPerson(t, "ev")
	legacyEventHandle := uniqueLabel(t, "my") + "--" + uniqueLabel(t, "event")
	setHandle(t, a, legacyEvent, legacyEventHandle)
	a.deploy(t, legacyEvent, "alpha")

	asHackFlat(a)
	ctx := context.Background()

	flatLabel := "alpha--" + legacyEventHandle
	site, handle, base, ok := a.sites.siteHostParts(ctx, flatLabel+"."+pcSiteDomain)
	if !ok || site != "alpha" || handle != legacyEventHandle || base != pcSiteDomain {
		t.Fatalf("siteHostParts(%q) = %q, %q, %q, %v; want alpha, %q, %q, true", flatLabel, site, handle, base, ok, legacyEventHandle, pcSiteDomain)
	}

	host := flatLabel + "." + pcSiteDomain
	r := a.at(t, "GET", host, "/", nil, nil)
	if r.status != 200 || !strings.Contains(string(r.body), "<h1>alpha</h1>") {
		t.Fatalf("flat host over a legacy double-hyphen event %s: %d %s", host, r.status, r.body)
	}
}

// TestOriginIsSiteHostIDFlatHosts (issue 3): same-origin authorization
// (originIsSiteHostID, used by CORS/API-scoping) recognizes the flat host
// as the site's own origin once flat hosts are on — and, since siteHostFor
// (the single shared construction) then hands out only that one form, the
// stale two-level host it used to match no longer does, matching off mode's
// own single-canonical-form behaviour in reverse.
func TestOriginIsSiteHostIDFlatHosts(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	event := a.newPerson(t, "ev")
	eventHandle := uniqueLabel(t, "weekend")
	setHandle(t, a, event, eventHandle)
	a.deploy(t, event, "alpha")
	markReady(t, dir, eventHandle) // off mode needs the certificate marker
	siteID := a.siteID(t, event, "alpha")
	ctx := context.Background()
	twoLevelHost := "alpha." + eventHandle + "." + pcSiteDomain
	flatHost := "alpha--" + eventHandle + "." + pcSiteDomain

	if !a.sites.originIsSiteHostID(ctx, siteID, twoLevelHost) {
		t.Fatalf("off: originIsSiteHostID(%q) = false", twoLevelHost)
	}

	asHackFlat(a)
	if !a.sites.originIsSiteHostID(ctx, siteID, flatHost) {
		t.Fatalf("flat: originIsSiteHostID(%q) = false", flatHost)
	}
	if a.sites.originIsSiteHostID(ctx, siteID, twoLevelHost) {
		t.Fatalf("flat: originIsSiteHostID(%q) = true; siteHostFor now hands out only the flat form", twoLevelHost)
	}
}
