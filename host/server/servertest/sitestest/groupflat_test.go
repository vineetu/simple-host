package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
	"github.com/vsriram/simple-host/host/web"
)

// Flat team-site hosts (<site>--<event>.<domain>): the fixture deploys under
// the default edition, then flips Sites to a hosted-events edition with
// TEAM_SITE_FLAT_HOSTS on. Real event lookup is products/hack's.

func groupEdition() *edition.Edition {
	return edition.FromPolicies(edition.RoutePolicy{HostedEvents: true}, edition.PresentationPolicy{ProductName: "Test product", ProductAssets: true, CanonicalBaseURL: "https://product.example", StylePath: "product.css", StyleClass: "product"}, edition.IdentityPolicy{RandomHandles: true, PaperMetrics: true}, edition.StoragePolicy{EventSites: true, StrictArchives: true, JudgePreviewFrames: true, EventVersionRetention: true, CombinedPool: true}, edition.NotificationPolicy{EventScoped: true}, config.DefaultLimits())
}

func (a *App) asGroupFlat() {
	a.Sites.Ed = groupEdition()
	a.Hosts.SetEdition(a.Sites.Ed)
	a.Sites.SetTeamSiteFlatHosts(true)
}

// withHandle gives p the handle h.
func (a *App) withHandle(t *testing.T, p rt.Person, h string) rt.Person {
	t.Helper()
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, h, p.ID); err != nil {
		t.Fatal(err)
	}
	p.Handle = h
	return p
}

// Flat hosts serve without a per-event certificate marker, split at the
// first "--" (a legacy event slug may hold one), and an account whose own
// handle holds "--" keeps its origin rather than being split into another
// account's site.
func TestSiteHostsFlatRouting(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	event := a.withHandle(t, a.NewPerson(t, "ev"), fixture.Uniq("weekend"))
	a.files(t, event, "POST", "alpha", map[string]string{"index.html": "<h1>alpha</h1>"})
	legacy := a.withHandle(t, a.NewPerson(t, "ev"), fixture.Uniq("my")+"--"+fixture.Uniq("event"))
	a.files(t, legacy, "POST", "alpha", map[string]string{"index.html": "<h1>legacy alpha</h1>"})
	other := a.withHandle(t, a.NewPerson(t, "ev"), fixture.Uniq("summer"))
	a.files(t, other, "POST", "red", map[string]string{"index.html": "<h1>red</h1>"})
	a.files(t, other, "POST", "blue", map[string]string{"index.html": "<h1>blue</h1>"})
	clash := a.withHandle(t, a.NewPerson(t, "legacy"), "red--"+other.Handle)
	a.Hosts.SetGroupHosts(&stubGroupHosts{
		group: web.Group{GroupRef: web.GroupRef{ID: "group", AccountID: clash.ID}},
		page: func(w http.ResponseWriter, r *http.Request, user model.User) bool {
			if user.ID != clash.ID {
				return false
			}
			_, _ = w.Write([]byte("<h1>legacy event page</h1>"))
			return true
		},
	})
	a.asGroupFlat()
	for host, want := range map[string]string{
		"alpha--" + event.Handle + "." + rt.SiteDomain:  "<h1>alpha</h1>",
		"alpha--" + legacy.Handle + "." + rt.SiteDomain: "<h1>legacy alpha</h1>",
		"blue--" + other.Handle + "." + rt.SiteDomain:   "<h1>blue</h1>",
		"red." + other.Handle + "." + rt.SiteDomain:     "<h1>red</h1>",
		clash.Handle + "." + rt.SiteDomain:              "<h1>legacy event page</h1>",
	} {
		if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), want) {
			t.Errorf("%s: %d %s, want %s", host, r.Status, r.Body, want)
		}
	}
	if site, handle, _, ok := a.Hosts.SiteHostParts(ctx, "alpha--"+legacy.Handle+"."+rt.SiteDomain); !ok || site != "alpha" || handle != legacy.Handle {
		t.Fatalf("legacy split: %q %q %v", site, handle, ok)
	}
	if _, _, _, ok := a.Hosts.SiteHostParts(ctx, clash.Handle+"."+rt.SiteDomain); ok {
		t.Fatalf("a real handle was split as a flat label")
	}
}

// Once flat hosts are on, the flat host is the site's own origin and the
// two-level one no longer is.
func TestOriginIsSiteHostIDFlatHosts(t *testing.T) {
	a := NewApp(t)
	event := a.withHandle(t, a.NewPerson(t, "ev"), fixture.Uniq("weekend"))
	siteID := a.Deploy(t, event, "alpha")
	a.MarkReady(t, event.Handle)
	ctx := context.Background()
	twoLevel, flat := event.SiteHost("alpha"), "alpha--"+event.Handle+"."+rt.SiteDomain
	if !a.Hosts.OriginIsSiteHost(ctx, siteID, twoLevel) {
		t.Fatalf("off: %q refused", twoLevel)
	}
	a.asGroupFlat()
	if !a.Hosts.OriginIsSiteHost(ctx, siteID, flat) || a.Hosts.OriginIsSiteHost(ctx, siteID, twoLevel) {
		t.Fatalf("flat: flat %v, two-level %v", a.Hosts.OriginIsSiteHost(ctx, siteID, flat), a.Hosts.OriginIsSiteHost(ctx, siteID, twoLevel))
	}
}
