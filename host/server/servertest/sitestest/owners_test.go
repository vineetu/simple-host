package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// newTeam makes a team account with handle h and the given members, as
// Access's team routes leave it.
func (a *App) newTeam(t *testing.T, h string, members ...rt.Person) string {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := accountsrepo.InsertTeamAccount(ctx, tx)
	if err == nil {
		_, err = tx.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, h, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id = $1`, id) })
	for _, m := range members {
		if _, err := accountsrepo.AddTeamMember(ctx, a.Database, id, m.ID, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// A team's members deploy, rename and roll back its sites (?owner=); an
// update with a stale If-Match is refused; a non-member gets nothing.
func TestTeamMembersManageTeamSites(t *testing.T) {
	a := NewApp(t)
	ann, cal := a.NewPerson(t, "team-ann"), a.NewPerson(t, "team-cal")
	team := fixture.Uniq("team")
	teamID := a.newTeam(t, team, ann)
	as := func(p rt.Person, method, path string, body any, extra ...string) rt.Resp {
		h := p.Header()
		for i := 0; i+1 < len(extra); i += 2 {
			h[extra[i]] = extra[i+1]
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return a.At(t, method, rt.SiteDomain, path+sep+"owner="+team, body, h)
	}
	files := func(s string) map[string]any { return map[string]any{"files": map[string]string{"index.html": s}} }
	if r := as(ann, "PUT", "/v1/sites/shop/files?create=1", files("v1")); r.Status/100 != 2 {
		t.Fatalf("member create: %d %s", r.Status, r.Body)
	}
	if a.SiteID(t, rt.Person{ID: teamID}, "shop") == "" {
		t.Fatal("the site is not the team's")
	}
	if r := as(ann, "PUT", "/v1/sites/shop/files", files("v2"), "If-Match", `"1"`); r.Status != 200 {
		t.Fatalf("matching If-Match: %d %s", r.Status, r.Body)
	}
	if r := as(ann, "PUT", "/v1/sites/shop/files", files("v3"), "If-Match", `"1"`); r.Status != http.StatusPreconditionFailed || r.Code() != "version_changed" {
		t.Fatalf("stale If-Match: %d %s", r.Status, r.Body)
	}
	if r := as(ann, "PUT", "/v1/sites/shop/active-version", map[string]int{"version_number": 1}); r.Status != 200 {
		t.Fatalf("member rollback: %d %s", r.Status, r.Body)
	}
	if r := as(ann, "PATCH", "/v1/sites/shop", map[string]string{"name": "store"}); r.Status != 200 {
		t.Fatalf("member rename: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{{"PUT", "/v1/sites/store/files", files("x")}, {"PUT", "/v1/sites/store/active-version", map[string]int{"version_number": 2}}, {"PATCH", "/v1/sites/store", map[string]string{"name": "mine"}}, {"DELETE", "/v1/sites/store", nil}} {
		if r := as(cal, c.method, c.path, c.body); r.Status/100 == 2 {
			t.Fatalf("non-member %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}

// An editor of one site may deploy it for its owner; not another site of
// that owner, and a stranger not at all.
func TestEditorDeploysOnlyTheirSite(t *testing.T) {
	a := NewApp(t)
	olive, ed, stranger := a.NewPerson(t, "olive"), a.NewPerson(t, "editor"), a.NewPerson(t, "stranger")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	if _, err := accountsrepo.SetSiteEditor(context.Background(), a.Database, olive.ID, "shop", ed.ID, olive.ID, true); err != nil {
		t.Fatal(err)
	}
	deploy := func(p rt.Person, site string) rt.Resp {
		return a.At(t, "PUT", rt.SiteDomain, "/v1/sites/"+site+"/files?owner="+olive.Handle, map[string]any{"files": map[string]string{"index.html": "by " + p.Handle}}, p.Header())
	}
	if r := deploy(ed, "shop"); r.Status != 200 {
		t.Fatalf("editor deploy: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/", nil, nil); string(r.Body) != "by "+ed.Handle {
		t.Fatalf("editor's deploy not live: %s", r.Body)
	}
	if r := deploy(ed, "blog"); r.Status/100 == 2 {
		t.Fatalf("editor deployed another site: %d %s", r.Status, r.Body)
	}
	if r := deploy(stranger, "shop"); r.Status/100 == 2 {
		t.Fatalf("stranger deployed: %d %s", r.Status, r.Body)
	}
}

// Admin routes answer only the admin: a person's full key, and a
// deploy-only key, get the admin's 404 on the admin site actions and the
// download-all.
func TestAdminRoutesRefuseOtherKeys(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "shop")
	a.files(t, olive, "PUT", "shop", map[string]string{"index.html": "v2"})
	for _, key := range []map[string]string{olive.Header(), a.deployKey(t, olive)} {
		for _, c := range []struct{ method, path string }{
			{"GET", "/v1/admin/sites"}, {"GET", "/v1/admin/sites/" + siteID + "/versions"},
			{"PUT", "/v1/admin/sites/" + siteID + "/active-version"}, {"DELETE", "/v1/admin/sites/" + siteID + "/versions/1"},
			{"DELETE", "/v1/admin/sites/" + siteID}, {"GET", "/v1/admin/export.tar.gz"},
		} {
			if r := a.At(t, c.method, rt.SiteDomain, c.path, map[string]int{"version_number": 1}, key); r.Status/100 == 2 {
				t.Errorf("%s %s with a non-admin key: %d", c.method, c.path, r.Status)
			}
		}
	}
	if r := a.admin(t, "GET", "/v1/admin/sites/"+siteID+"/versions", nil); r.Status != 200 {
		t.Fatalf("admin: %d %s", r.Status, r.Body)
	}
	if a.rowCount(t, siteID) != 1 {
		t.Fatal("a refused admin delete removed the site")
	}
}

// A protected site is never served on a claimed name or a verified custom
// domain either.
func TestProtectedSiteOnOwnAddresses(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	shopID := a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	claimed := fixture.Uniq("olv") + "." + rt.SiteDomain
	custom := fixture.Uniq("blog") + ".example.test"
	a.bind(t, olive, "shop", claimed)
	a.bind(t, olive, "blog", custom)
	a.verify(t, a.SiteID(t, olive, "blog"), custom)
	_ = shopID
	for site, host := range map[string]string{"shop": claimed, "blog": custom} {
		path := "/"
		if site == "blog" {
			path = "/internal/passcode/" // nginx's rewrite for a marked site on its own domain
		}
		if r := a.At(t, "GET", host, path, nil, nil); r.Status != 200 {
			t.Fatalf("%s before: %d %s", host, r.Status, r.Body)
		}
		a.protect(t, olive, site)
		for _, p := range []string{"/", "/internal/passcode/"} {
			if r := a.At(t, "GET", host, p, nil, nil); r.Status == 200 || strings.Contains(string(r.Body), "<h1>"+site+"</h1>") {
				t.Fatalf("protected %s%s: %d %s", host, p, r.Status, r.Body)
			}
		}
	}
}

// Creating a site whose name the account already has answers site_exists
// (POST JSON and archive), and changes nothing.
func TestCreateExistingSiteRefused(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.files(t, olive, "POST", "shop", map[string]string{"index.html": "again"}); r.Status != http.StatusConflict || r.Code() != "site_exists" {
		t.Fatalf("POST files: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/shop", fixture.TarGzOf(t, 10), olive.Header()); r.Status != http.StatusConflict || r.Code() != "site_exists" {
		t.Fatalf("POST archive: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/", nil, nil); string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("live changed: %s", r.Body)
	}
}
