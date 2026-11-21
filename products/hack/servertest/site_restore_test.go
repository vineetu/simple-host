package servertest

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func TestRestoreTrashedTeamKeepsTakedown(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "org"), a.newPerson(t, "member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Restore team", member)
	key := member.key
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy", a.deployTeam(t, slug, team, key, "restored team content"), 201, "")
	ctx := context.Background()
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	svc := sites.New(a.database, a.disk, nil, 0, sites.Deps{GroupGate: teams.SiteGate{}, Sitemap: hackevents.FilterSitemapSites})
	checkSitemap := func(want bool) {
		t.Helper()
		list, err := svc.ListSitemapSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, site := range list {
			if site.UserID == accountID && site.Name == team {
				found = true
			}
		}
		if found != want {
			t.Fatalf("team in sitemap=%v, want %v", found, want)
		}
	}
	checkSitemap(true)
	if err := a.sites.TrashTeamSite(ctx, accountID, team); err != nil {
		t.Fatal(err)
	}
	base := "/v1/hack/events/" + slug + "/teams/" + team
	wantTS(t, "take down in trash", a.api(t, "POST", base+"/takedown", map[string]string{"reason": "spam"}, org.key), 200, "")
	d, err := siterepo.GetDeletedSiteByUser(ctx, a.database, accountID, team)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := svc.RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Suspended() || restored.SiteSuspendedReason != "Taken down by the event organiser: spam" {
		t.Fatalf("restored team lost take-down: suspended=%v reason=%q", restored.Suspended(), restored.SiteSuspendedReason)
	}
	if !a.disk.IsSuspended(accountID, team) {
		t.Fatal("restored team has no suspension marker")
	}
	host := team + "." + slug + "." + tsDomain
	if r := a.on(t, "GET", host, "/", nil, ""); r.status == 200 || strings.Contains(string(r.body), "restored team content") {
		t.Fatalf("taken-down page served: %d %s", r.status, r.body)
	}
	checkSitemap(false)
	wantTS(t, "organiser restore", a.api(t, "POST", base+"/restore", nil, org.key), 200, "")
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "restored team content") {
		t.Fatalf("organiser restore did not restore page: %d %s", r.status, r.body)
	}
	checkSitemap(true)
}
