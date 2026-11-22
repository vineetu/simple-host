package sitestest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// writeLive writes one file into p's site's live tree.
func (a *App) writeLive(t *testing.T, p rt.Person, site, name, content string) {
	t.Helper()
	path := filepath.Join(a.siteDir(p, site), "current", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (a *App) setHome(t *testing.T, p rt.Person, site string) {
	t.Helper()
	if err := sitesrepo.SetHomeSite(context.Background(), a.Database, p.ID, &site); err != nil {
		t.Fatal(err)
	}
}

// deployKey is a deploy-only key for p.
func (a *App) deployKey(t *testing.T, p rt.Person) map[string]string {
	t.Helper()
	k, _ := auth.GenerateAPIKey()
	if _, err := a.Database.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, accountsrepo.HashAPIKey(k), p.ID); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"X-API-Key": k}
}

// The owner chooses one of their sites as the person host's home: it serves
// at /, other sites' old paths redirect to their own hosts, the home's own
// files win, and only the owner's full key may choose.
func TestHomeSettingServingAndIsolation(t *testing.T) {
	a := NewApp(t)
	owner, other := a.NewPerson(t, "homeowner"), a.NewPerson(t, "otherhome")
	id := a.Deploy(t, owner, "home")
	a.Deploy(t, owner, "blog")
	a.Deploy(t, other, "foreign")
	host := owner.Handle + "." + rt.SiteDomain
	setHome := func(h map[string]string, site any) rt.Resp {
		return a.At(t, "PUT", rt.SiteDomain, "/v1/me/home", map[string]any{"site": site}, h)
	}
	for _, bad := range []any{4, "../home"} {
		if r := setHome(owner.Header(), bad); r.Status != 400 {
			t.Fatalf("invalid %v: %d", bad, r.Status)
		}
	}
	if r := setHome(owner.Header(), "foreign"); r.Status != 404 {
		t.Fatalf("foreign: %d", r.Status)
	}
	if r := setHome(nil, "home"); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
	if r := setHome(a.deployKey(t, owner), "home"); r.Status != 403 {
		t.Fatalf("deploy key: %d", r.Status)
	}
	if r := setHome(owner.Header(), "home"); r.Status != 200 {
		t.Fatalf("set: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>home</h1>" {
		t.Fatalf("home: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("sub: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/blog/sub/?q=1", nil, nil); r.Status != 302 || location(r) != "https://blog."+host+"/sub/?q=1" {
		t.Fatalf("collision: %d %s", r.Status, location(r))
	}
	// A file wins over the old path link, and the site's own 404 is used.
	a.writeLive(t, owner, "home", "blog/index.html", "home blog")
	a.writeLive(t, owner, "home", "404.html", "home 404")
	if r := a.At(t, "GET", host, "/blog/", nil, nil); string(r.Body) != "home blog" {
		t.Fatalf("file collision: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/missing", nil, nil); r.Status != 404 || string(r.Body) != "home 404" {
		t.Fatalf("own 404: %d %s", r.Status, r.Body)
	}
	if got, ok := a.Hosts.ReturnSite(context.Background(), host, "/any/deep/path"); !ok || got != id {
		t.Fatal("OAuth return did not bind home")
	}
	if r := a.api(t, owner, "PATCH", "/v1/sites/home", map[string]any{"name": "renamedhome"}); r.Status != 200 {
		t.Fatalf("rename: %s", r.Body)
	}
	if s, has, err := sitesrepo.HomeSite(context.Background(), a.Database, owner.ID); err != nil || !has || s.ID != id {
		t.Fatalf("rename lost home: %+v %v", s, err)
	}
	if r := setHome(owner.Header(), nil); r.Status != 200 {
		t.Fatalf("unset: %s", r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); !strings.Contains(string(r.Body), "SHOWCASE") {
		t.Fatal("showcase not restored")
	}
}

// A home that cannot be served falls back to the showcase; the setting
// survives what can be undone and goes with a deleted site.
func TestHomeFallbacks(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	for _, kind := range []string{"offline", "taken-down", "suspended", "recently-deleted", "deleted"} {
		owner := a.NewPerson(t, "fallback")
		id := a.Deploy(t, owner, "home")
		a.setHome(t, owner, "home")
		var err error
		switch kind {
		case "offline":
			err = sitesrepo.SetSiteOffline(ctx, a.Database, id, true)
		case "taken-down":
			err = sitesrepo.SetSiteSuspended(ctx, a.Database, id, "test")
		case "suspended":
			err = accountsrepo.SetUserSuspended(ctx, a.Database, owner.ID, "test")
		case "recently-deleted":
			err = sitesrepo.MarkSiteDeleted(ctx, a.Database, id, 7*24*time.Hour)
		case "deleted":
			_, err = a.Database.Exec(`DELETE FROM sites WHERE id=$1`, id)
		}
		if err != nil {
			t.Fatal(err)
		}
		if r := a.At(t, "GET", owner.Handle+"."+rt.SiteDomain, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "SHOWCASE") || strings.Contains(string(r.Body), "<h1>home</h1>") {
			t.Fatalf("fallback %s: %d", kind, r.Status)
		}
		_, has, err := sitesrepo.HomeSite(ctx, a.Database, owner.ID)
		if want := kind != "recently-deleted" && kind != "deleted"; err != nil || has != want {
			t.Fatalf("%s: setting retained=%v want=%v %v", kind, has, want, err)
		}
	}
}

// A protected home is gated on its person host, and under a flat base the
// person host of the other base only redirects there.
func TestHomeBaseAndPasscode(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "lockedhome")
	a.Deploy(t, owner, "home")
	a.setHome(t, owner, "home")
	a.Hosts.SetSiteBase("simple-host.alt", "flat")
	person := owner.Handle + "." + rt.SiteDomain
	if r := a.At(t, "GET", person, "/", nil, nil); r.Status != 200 {
		t.Fatalf("open home: %d", r.Status)
	}
	if r := a.At(t, "GET", owner.Handle+".simple-host.alt", "/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+person+"/" {
		t.Fatalf("other base: %d %q", r.Status, r.Header.Get("Location"))
	}
	a.protect(t, owner, "home")
	if r := a.At(t, "GET", person, "/", nil, nil); r.Status != 403 || strings.Contains(string(r.Body), "<h1>home</h1>") {
		t.Fatalf("gate: %d", r.Status)
	}
}

// While the person's site-host certificate is pending, another site's path
// never runs on the home's origin.
func TestHomePendingCertificateCollisionIsolation(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "pendinghome")
	a.Deploy(t, owner, "home")
	a.Deploy(t, owner, "other")
	if err := os.Remove(filepath.Join(a.CertDir, "ready", owner.Handle)); err != nil {
		t.Fatal(err)
	}
	a.setHome(t, owner, "home")
	host := owner.Handle + "." + rt.SiteDomain
	if r := a.At(t, "GET", host, "/other/", nil, nil); r.Status != 503 || strings.Contains(string(r.Body), "<h1>other</h1>") {
		t.Fatalf("other site executed on home: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/home/", nil, nil); r.Status != 302 || location(r) != "/" {
		t.Fatalf("home old path: %d %q", r.Status, location(r))
	}
}

// Without person hosts (the path model) the showcase address redirects to
// the chosen home, and the feed still carries the bio.
func TestSmallBoxPathHomeAndCuration(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetPersonHosts("off")
	a.Hosts.SetSiteHosts("off", "")
	owner := a.NewPerson(t, "boxowner")
	a.Deploy(t, owner, "home")
	for path, body := range map[string]any{"/v1/me/home": map[string]any{"site": "home"}, "/v1/me/bio": map[string]any{"bio": "Small box"}} {
		if r := a.api(t, owner, "PUT", path, body); r.Status != 200 {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
	}
	showcase := func() *httptest.ResponseRecorder {
		req, _ := http.NewRequest("GET", "https://"+rt.ContentHost+"/", nil)
		req.SetPathValue("handle", owner.Handle)
		w := httptest.NewRecorder()
		a.Sites.Showcase(w, req)
		return w
	}
	if w := showcase(); w.Code != 302 || w.Header().Get("Location") != a.Sites.SiteURL(owner.Handle, "home") {
		t.Fatalf("path home: %d %s", w.Code, w.Header().Get("Location"))
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/u/"+owner.Handle+"/showcase.json", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Small box") {
		t.Fatalf("feed: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, owner, "PUT", "/v1/me/home", map[string]any{"site": nil}); r.Status != 200 {
		t.Fatalf("clear: %d", r.Status)
	}
	if w := showcase(); w.Code != 200 {
		t.Fatalf("showcase fallback: %d", w.Code)
	}
}
