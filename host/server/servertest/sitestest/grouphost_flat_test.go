package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/model"
)

// --- DB-backed routing tests -------------------------------------------
//
// These build the fixture under Host edition (so the generic deploy helper
// is unrestricted), then flip the handler's edition to Hack and turn flat
// hosts on: siteHostParts/SiteHosts/siteCertReadyOn read h.ed and the flag
// live, so this exactly simulates a hosted-events instance with
// TEAM_SITE_FLAT_HOSTS on, without needing full event/team machinery.

func asGroupFlat(a *servertest.PrivateApp) {
	a.Sites.Ed = servertest.GroupTestEdition()
	a.Access.Ed, a.Data.Ed = a.Sites.Ed, a.Sites.Ed
	a.Sites.Hosts.SetEdition(a.Sites.Ed)
	a.Sites.SetTeamSiteFlatHosts(true)
}

func setHandle(t *testing.T, a *servertest.PrivateApp, p servertest.Person, handle string) {
	t.Helper()
	id, _ := a.UserID(t, p)
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, id); err != nil {
		t.Fatal(err)
	}
}

// makeEventAccount installs a group stub: this half tests platform dispatch.
// Real event lookup and flat-host routing are tested in products/hack/servertest.
func makeEventAccount(t *testing.T, a *servertest.PrivateApp, p servertest.Person) {
	t.Helper()
	id, _ := a.UserID(t, p)
	a.Sites.Hosts.SetGroupHosts(&stubGroupHosts{
		group: web.Group{GroupRef: web.GroupRef{ID: "group", AccountID: id}},
		page: func(w http.ResponseWriter, r *http.Request, user model.User) bool {
			if user.ID != id {
				return false
			}
			w.Write([]byte("<h1>legacy event page</h1>"))
			return true
		},
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
	a, _ := servertest.NewSiteApp(t, "canonical")
	event := a.NewPerson(t, "ev")
	eventHandle := servertest.UniqueLabel(t, "weekend")
	setHandle(t, a, event, eventHandle)
	a.Deploy(t, event, "alpha")

	asGroupFlat(a)

	host := "alpha--" + eventHandle + "." + servertest.PcSiteDomain
	r := a.At(t, "GET", host, "/", nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), "<h1>alpha</h1>") {
		t.Fatalf("flat host %s: %d %s", host, r.Status, r.Body)
	}
}

// TestSiteHostsFlatModeExactHandleWins (issue 2): a pre-existing account
// whose handle literally contains "--" (an event slug created before "--"
// was forbidden) keeps its own origin, even when splitting its label on
// "--" would otherwise resolve to a real team's site under a different
// event.
func TestSiteHostsFlatModeExactHandleWins(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	otherEvent := a.NewPerson(t, "ev")
	legacyEvent := a.NewPerson(t, "legacy")
	otherHandle := servertest.UniqueLabel(t, "summer")
	setHandle(t, a, otherEvent, otherHandle)
	// The dangerous collision: a real site named "red" under the OTHER
	// event's handle, matching exactly what splitting the legacy handle on
	// "--" would produce. "blue" is a second, uninvolved site used below to
	// show the flat split still works for a label that is not a real handle.
	a.Deploy(t, otherEvent, "red")
	a.Deploy(t, otherEvent, "blue")
	legacyHandle := "red--" + otherHandle
	setHandle(t, a, legacyEvent, legacyHandle)
	// legacyEvent is a real hosted event (not just a bare account), so its
	// own host can actually be checked to serve the event, not merely to
	// not serve "red".
	makeEventAccount(t, a, legacyEvent)

	asGroupFlat(a)
	ctx := context.Background()

	if _, _, _, ok := a.Sites.Hosts.SiteHostParts(ctx, legacyHandle+"."+servertest.PcSiteDomain); ok {
		t.Fatalf("siteHostParts resolved %q as a flat team label despite it being a real handle", legacyHandle)
	}

	host := legacyHandle + "." + servertest.PcSiteDomain
	r := a.At(t, "GET", host, "/", nil, nil)
	if strings.Contains(string(r.Body), "<h1>red</h1>") {
		t.Fatalf("existing handle %q was hijacked into serving another account's %q site: %d %s", legacyHandle, "red", r.Status, r.Body)
	}
	// Not just "didn't serve red": it must actually serve the legacy
	// event's own page.
	if r.Status != 200 || !strings.Contains(string(r.Body), "<h1>legacy event page</h1>") {
		t.Fatalf("legacy event host %s did not serve the event: %d %s", host, r.Status, r.Body)
	}

	// Contrast: a genuine flat label (not a real handle) still splits and
	// resolves to the real team site.
	genuineHost := "blue--" + otherHandle + "." + servertest.PcSiteDomain
	r2 := a.At(t, "GET", genuineHost, "/", nil, nil)
	if r2.Status != 200 || !strings.Contains(string(r2.Body), "<h1>blue</h1>") {
		t.Fatalf("genuine flat host %s: %d %s", genuineHost, r2.Status, r2.Body)
	}

	// The two-level form for the same team is unaffected by flat mode.
	twoLevelHost := "red." + otherHandle + "." + servertest.PcSiteDomain
	r3 := a.At(t, "GET", twoLevelHost, "/", nil, nil)
	if r3.Status != 200 || !strings.Contains(string(r3.Body), "<h1>red</h1>") {
		t.Fatalf("two-level host %s under flat mode: %d %s", twoLevelHost, r3.Status, r3.Body)
	}
}

// TestSiteHostPartsFlatHostsLegacyEventSlugWithDoubleHyphen (issue 2): a
// legacy event whose own slug/handle contains "--" (predating the rule that
// forbids it) still gets correctly split at the FIRST "--": the team part
// never contains "--" by construction, so everything after it, "--" and
// all, is the event.
func TestSiteHostPartsFlatHostsLegacyEventSlugWithDoubleHyphen(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	legacyEvent := a.NewPerson(t, "ev")
	legacyEventHandle := servertest.UniqueLabel(t, "my") + "--" + servertest.UniqueLabel(t, "event")
	setHandle(t, a, legacyEvent, legacyEventHandle)
	a.Deploy(t, legacyEvent, "alpha")

	asGroupFlat(a)
	ctx := context.Background()

	flatLabel := "alpha--" + legacyEventHandle
	site, handle, base, ok := a.Sites.Hosts.SiteHostParts(ctx, flatLabel+"."+servertest.PcSiteDomain)
	if !ok || site != "alpha" || handle != legacyEventHandle || base != servertest.PcSiteDomain {
		t.Fatalf("siteHostParts(%q) = %q, %q, %q, %v; want alpha, %q, %q, true", flatLabel, site, handle, base, ok, legacyEventHandle, servertest.PcSiteDomain)
	}

	host := flatLabel + "." + servertest.PcSiteDomain
	r := a.At(t, "GET", host, "/", nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), "<h1>alpha</h1>") {
		t.Fatalf("flat host over a legacy double-hyphen event %s: %d %s", host, r.Status, r.Body)
	}
}

// TestOriginIsSiteHostIDFlatHosts (issue 3): same-origin authorization
// (originIsSiteHostID, used by CORS/API-scoping) recognizes the flat host
// as the site's own origin once flat hosts are on — and, since siteHostFor
// (the single shared construction) then hands out only that one form, the
// stale two-level host it used to match no longer does, matching off mode's
// own single-canonical-form behaviour in reverse.
func TestOriginIsSiteHostIDFlatHosts(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	event := a.NewPerson(t, "ev")
	eventHandle := servertest.UniqueLabel(t, "weekend")
	setHandle(t, a, event, eventHandle)
	a.Deploy(t, event, "alpha")
	servertest.MarkReady(t, dir, eventHandle) // off mode needs the certificate marker
	siteID := a.SiteID(t, event, "alpha")
	ctx := context.Background()
	twoLevelHost := "alpha." + eventHandle + "." + servertest.PcSiteDomain
	flatHost := "alpha--" + eventHandle + "." + servertest.PcSiteDomain

	if !a.Sites.Hosts.OriginIsSiteHost(ctx, siteID, twoLevelHost) {
		t.Fatalf("off: originIsSiteHostID(%q) = false", twoLevelHost)
	}

	asGroupFlat(a)
	if !a.Sites.Hosts.OriginIsSiteHost(ctx, siteID, flatHost) {
		t.Fatalf("flat: originIsSiteHostID(%q) = false", flatHost)
	}
	if a.Sites.Hosts.OriginIsSiteHost(ctx, siteID, twoLevelHost) {
		t.Fatalf("flat: originIsSiteHostID(%q) = true; siteHostFor now hands out only the flat form", twoLevelHost)
	}
}
