package servertest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	siterepo "github.com/vsriram/simple-host/host/repository/sites"
	"github.com/vsriram/simple-host/host/static"
	"github.com/vsriram/simple-host/host/storage"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func TestStaticHackOwnerFrameHeaders(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "static-org"), a.newPerson(t, "static-member")
	slug := a.makeEvent(t, org)
	markReady(t, a.certDir, slug)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Static", member)
	wantTS(t, "deploy", a.deployTeam(t, team, a.teamKey(t, slug, member), "static"), 201, "")
	sid := hackSiteID(t, a, slug, team)
	host := team + "." + slug + "." + tsDomain
	wantTS(t, "event website", a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/files?create=1", deployBody("static event"), org.key), 201, "")
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	eventSite := hackSiteID(t, a, slug, eventID)
	table, _, err := siterepo.LoadSiteTable(context.Background(), a.database, hackevents.LoadRoutingFacts)
	if err != nil {
		t.Fatal(err)
	}
	front := staticHackFront(t, a, table, false)
	original := a.srv
	for _, who := range []struct {
		name, site, host string
		person           person
		guarded          bool
	}{
		{"member-team", sid, host, member, true},
		{"organiser-team", sid, host, org, false},
		{"organiser-website", eventSite, slug + "." + tsDomain, org, true},
	} {
		t.Run(who.name, func(t *testing.T) {
			get := hackSession(t, a, a.uid(t, who.person), who.site, who.host)
			a.srv = original
			direct := get("GET", "/?q=one%2Ftwo", nil)
			a.srv = front
			via := get("GET", "/?q=one%2Ftwo", nil)
			frameOptions := direct.header.Get("X-Frame-Options") == "SAMEORIGIN"
			frameAncestors := strings.Contains(strings.Join(direct.header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'")
			if direct.status != 200 || frameOptions != who.guarded || frameAncestors != who.guarded {
				t.Fatalf("direct owner guard: %d %v", direct.status, direct.header)
			}
			for _, key := range []string{"X-Frame-Options", "Content-Security-Policy", "Cache-Control", "Vary"} {
				if strings.Join(via.header.Values(key), ";") != strings.Join(direct.header.Values(key), ";") {
					t.Errorf("%s direct=%v via=%v", key, direct.header.Values(key), via.header.Values(key))
				}
			}
			if via.status != direct.status || string(via.body) != string(direct.body) {
				t.Fatalf("owner page direct=%d via=%d", direct.status, via.status)
			}
		})
	}
	a.srv = original
}

func staticHackFront(t *testing.T, a *teamSiteApp, table model.SiteTable, flat bool) *httptest.Server {
	t.Helper()
	disk, err := storage.OpenReadOnly(a.disk.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	// The Static process deliberately does not run Hack's general wiring.
	h := handler.NewSiteHandler(productedition.ForKind(productedition.Hack), "", a.database, disk, tsDomain, "sites."+tsDomain, "", "", "", "", nil, 0, "", nil, nil)
	installAssets(h, nil)
	h.SetTeamSiteFlatHosts(flat)
	h.SetPersonHosts("canonical")
	h.SetSiteHosts("canonical", a.certDir)
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")
	httpx.SetTrustedProxies([]*net.IPNet{trusted})
	t.Cleanup(func() { httpx.SetTrustedProxies(nil) })
	target, _ := url.Parse(a.srv.URL)
	copy := static.New(static.Options{DataDir: disk.DataDir(), StateDir: t.TempDir(), Product: "simple-hack", Lookup: h.LookupRoutingSite, Logf: func(string, ...any) {}, Load: func(context.Context) (model.SiteTable, time.Time, error) { return table, time.Now(), nil }})
	if err := copy.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(static.NewHandler(copy, static.NewProxy(target, []*net.IPNet{trusted}), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		return h.StaticPages(table, api, retry)
	}, h.StaticAPIRequest))
	t.Cleanup(front.Close)
	return front
}

func TestStaticFlatTeamRootDuringAPIOutage(t *testing.T) {
	a := newTeamSiteApp(t)
	a.sites.SetTeamSiteFlatHosts(true)
	org, member := a.newPerson(t, "static-flat-org"), a.newPerson(t, "static-flat-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Static flat", member)
	wantTS(t, "publish", a.deployTeam(t, team, a.teamKey(t, slug, member), "cached flat root"), 201, "")
	host := team + "--" + slug + "." + tsDomain
	direct := a.on(t, "GET", host, "/", nil, "")
	table, _, err := siterepo.LoadSiteTable(context.Background(), a.database, hackevents.LoadRoutingFacts)
	if err != nil {
		t.Fatal(err)
	}
	api := a.srv
	front := staticHackFront(t, a, table, true)
	api.Close()
	a.srv = front
	via := a.on(t, "GET", host, "/", nil, "")
	if direct.status != 200 || via.status != direct.status || string(via.body) != string(direct.body) || !strings.Contains(string(via.body), "cached flat root") {
		t.Fatalf("flat root direct=%d via=%d %s", direct.status, via.status, via.body)
	}
}

func TestStaticHackErrorPageText(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "static-error-org"), a.newPerson(t, "static-error-member")
	slug := a.makeEvent(t, org)
	markReady(t, a.certDir, slug)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Static errors", member)
	wantTS(t, "publish", a.deployTeam(t, team, a.teamKey(t, slug, member), "unready team"), 201, "")
	if err := os.Remove(filepath.Join(a.certDir, "ready", slug)); err != nil {
		t.Fatal(err)
	}
	table, _, err := siterepo.LoadSiteTable(context.Background(), a.database, hackevents.LoadRoutingFacts)
	if err != nil {
		t.Fatal(err)
	}
	api := a.srv
	front := staticHackFront(t, a, table, false)
	for _, tc := range []struct{ name, host, path string }{
		{"unknown", "unknown-static." + tsDomain, "/missing"},
		{"unready-team", team + "." + slug + "." + tsDomain, "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.srv = api
			direct := a.on(t, "GET", tc.host, tc.path, nil, "")
			a.srv = front
			via := a.on(t, "GET", tc.host, tc.path, nil, "")
			if direct.status != 404 || !strings.Contains(string(direct.body), "Event not found") || !strings.Contains(string(direct.body), "No event or page lives at this address.") {
				t.Fatalf("direct error: %d %s", direct.status, direct.body)
			}
			if via.status != direct.status || string(via.body) != string(direct.body) {
				t.Fatalf("error direct=%d via=%d %s", direct.status, via.status, via.body)
			}
		})
	}
	a.srv = api
}
