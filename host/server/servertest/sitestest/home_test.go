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
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

func TestHomeSettingServingAndIsolation(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	a.Sites.Hosts.SetSiteHosts("canonical", "")
	owner := a.NewPerson(t, "homeowner")
	other := a.NewPerson(t, "otherhome")
	a.Deploy(t, owner, "home")
	a.Deploy(t, owner, "blog")
	a.Deploy(t, other, "foreign")
	uid, handle := a.UserID(t, owner)
	_, oh := a.UserID(t, other)
	host := handle + "." + servertest.PcSiteDomain
	headers := map[string]string{"X-API-Key": owner.Key}
	for _, body := range []any{map[string]any{}, map[string]any{"site": 4}, map[string]any{"site": "../home"}} {
		if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", body, headers); r.Status != 400 {
			t.Fatalf("invalid: %d", r.Status)
		}
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "foreign"}, headers); r.Status != 404 {
		t.Fatalf("foreign: %d", r.Status)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, nil); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.Status != 200 {
		t.Fatalf("set: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/me", nil, headers); r.JSON(t)["home_site"] != "home" {
		t.Fatalf("me: %s", r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>home</h1>" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("home: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("sub: %d", r.Status)
	}
	if r := a.At(t, "GET", "home."+host, "/", nil, nil); r.Status != 200 {
		t.Fatalf("normal host: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/blog/sub/?q=1", nil, nil); r.Status != 302 || r.Header.Get("Location") != "https://blog."+host+"/sub/?q=1" {
		t.Fatalf("collision: %d %s", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", host, "/missing", nil, nil); r.Status != 404 {
		t.Fatalf("404: %d", r.Status)
	}
	id := a.SiteID(t, owner, "home")
	// A file wins over the old path link, and the site's own 404 is used.
	if err := writeTestHomeFile(a.Sites, id, "blog.html", "home blog file"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/blog", nil, nil); r.Status != 200 || string(r.Body) != "home blog file" {
		t.Fatal(r.Status, string(r.Body))
	}
	for name, content := range map[string]string{"blog/index.html": "home blog", "404.html": "home 404"} {
		if err := writeTestHomeFile(a.Sites, id, name, content); err != nil {
			t.Fatal(err)
		}
	}
	if r := a.At(t, "GET", host, "/blog/", nil, nil); string(r.Body) != "home blog" {
		t.Fatalf("file collision: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/missing", nil, nil); r.Status != 404 || string(r.Body) != "home 404" {
		t.Fatalf("own 404: %d %s", r.Status, r.Body)
	}

	if r := a.At(t, "GET", host, "/v1/sites/"+handle+"/me", nil, servertest.Browser(host, "")); r.Status != 200 {
		t.Fatalf("helper host alias: %d", r.Status)
	}
	vic := a.NewPerson(t, "homevisitor")
	cookie := a.Session(t, vic, id, host)
	// Storage resources save on the home origin with the visitor cookie.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/home/storage/resources/form", servertest.StorageRules("kv", servertest.RulesSignedInWrite), headers); r.Status != 201 {
		t.Fatalf("resource: %d %s", r.Status, r.Body)
	}
	save := "/v1/sites/home/storage/kv/form/keys/message"
	if r := a.At(t, "PUT", host, save, map[string]any{"value": "saved on home"}, servertest.Browser(host, cookie)); r.Status != 200 {
		t.Fatalf("storage save: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/v1/sites/blog/me", "/v1/u/" + oh + "/sites/foreign/me"} {
		if r := a.At(t, "GET", host, p, nil, servertest.Browser(host, cookie)); r.Status == 200 {
			t.Fatalf("unscoped: %s", p)
		}
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/u/"+handle+"/sites/blog/me", nil, map[string]string{"Origin": "https://" + host}); r.Status == 200 {
		t.Fatal("home origin reads blog on apex")
	}
	if r := a.At(t, "GET", host, "/v1/me", nil, servertest.Browser(host, cookie)); r.Status != 401 {
		t.Fatalf("visitor became owner: %d", r.Status)
	}
	otherCookie := a.Session(t, vic, id, "home."+host)
	if r := a.At(t, "PUT", host, save, map[string]any{"value": "other"}, servertest.Browser(host, otherCookie)); r.Status/100 == 2 {
		t.Fatal("site session accepted on home")
	}
	if r := a.At(t, "GET", host, "/v1/sites/home/storage/kv/form/keys/message", nil, servertest.Browser(host, cookie)); r.Status != 200 || r.JSON(t)["value"] != "saved on home" {
		t.Fatalf("storage read: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/blog/storage/resources", nil, servertest.Browser(host, cookie)); r.Status == 200 {
		t.Fatal("storage crossed sites")
	}
	if id, ok := a.Sites.Hosts.ReturnSite(context.Background(), host, "/any/deep/path"); !ok || id != a.SiteID(t, owner, "home") {
		t.Fatal("OAuth return did not bind home")
	}
	// A selected home does not inherit the site's normal-host session, nor another home site's cookie.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "blog"}, headers); r.Status != 200 {
		t.Fatal("switch failed")
	}
	if r := a.At(t, "GET", host, "/v1/sites/blog/me", nil, servertest.Browser(host, cookie)); r.JSON(t)["signed_in"] == true {
		t.Fatal("home session widened after switch")
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.Status != 200 {
		t.Fatal("switch back failed")
	}
	// Deploy scope is enforced both in the route and in the global scope gate.
	deployKey, _ := auth.GenerateAPIKey()
	if _, err := a.Database.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, accountsrepo.HashAPIKey(deployKey), uid); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, map[string]string{"X-API-Key": deployKey}); r.Status != 403 {
		t.Fatalf("deploy key: %d", r.Status)
	}
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/home", map[string]any{"name": "renamedhome"}, headers); r.Status != 200 {
		t.Fatalf("rename: %s", r.Body)
	}
	if s, has, err := sitesrepo.HomeSite(context.Background(), a.Database, uid); err != nil || !has || s.ID != id || s.Name != "renamedhome" {
		t.Fatalf("rename lost home: %+v %v", s, err)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, headers); r.Status != 200 {
		t.Fatalf("unset: %s", r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); !strings.Contains(string(r.Body), "SHOWCASE") {
		t.Fatal("showcase not restored")
	}
}

func TestHomeFallbacks(t *testing.T) {
	for _, kind := range []string{"offline", "taken-down", "suspended", "recently-deleted", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			a := servertest.NewPersonApp(t, "canonical")
			a.Sites.Hosts.SetSiteHosts("canonical", "")
			owner := a.NewPerson(t, "fallback")
			a.Deploy(t, owner, "home")
			uid, handle := a.UserID(t, owner)
			name := "home"
			if err := sitesrepo.SetHomeSite(context.Background(), a.Database, uid, &name); err != nil {
				t.Fatal(err)
			}
			id := a.SiteID(t, owner, "home")
			var err error
			switch kind {
			case "offline":
				err = sitesrepo.SetSiteOffline(context.Background(), a.Database, id, true)
			case "taken-down":
				err = sitesrepo.SetSiteSuspended(context.Background(), a.Database, id, "test")
			case "suspended":
				err = accountsrepo.SetUserSuspended(context.Background(), a.Database, uid, "test")
			case "recently-deleted":
				err = sitesrepo.MarkSiteDeleted(context.Background(), a.Database, id, 7*24*time.Hour)
			case "deleted":
				_, err = a.Database.Exec(`DELETE FROM sites WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if r := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "SHOWCASE") {
				t.Fatalf("fallback %s: %d", kind, r.Status)
			}
			_, has, err := sitesrepo.HomeSite(context.Background(), a.Database, uid)
			want := kind != "recently-deleted" && kind != "deleted"
			if err != nil || has != want {
				t.Fatalf("setting retained=%v want=%v %v", has, want, err)
			}
		})
	}
}

func TestHomeBaseAndPasscode(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	a.Sites.Hosts.SetSiteHosts("canonical", "")
	owner := a.NewPerson(t, "lockedhome")
	a.Deploy(t, owner, "home")
	uid, handle := a.UserID(t, owner)
	name := "home"
	if err := sitesrepo.SetHomeSite(context.Background(), a.Database, uid, &name); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.SetSiteBase("simple-host.alt", "serve", "")
	// Existing gate and generation cookies apply independently on the two hosts.
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-API-Key": owner.Key}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/home/storage/resources/notes", servertest.StorageRules("kv", servertest.RulesPublic), headers); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/home/lock", map[string]any{"passcode": "123456"}, headers); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	var firstCookie string
	for _, base := range []string{servertest.PcSiteDomain, "simple-host.alt"} {
		host := handle + "." + base
		if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 401 {
			t.Fatalf("gate on %s: %d", host, r.Status)
		}
		if r := a.At(t, "GET", host, "/v1/sites/home/storage/kv/notes/keys", nil, servertest.Browser(host, "")); r.Status != 403 {
			t.Fatalf("data gate: %d", r.Status)
		}
		if firstCookie != "" {
			if r := a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": firstCookie}); r.Status != 401 {
				t.Fatal("unlock crossed base hosts")
			}
		}
		unlocked := a.At(t, "POST", host, "/v1/site-unlock", "passcode=123456&next=%2F", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"})
		if unlocked.Status != 303 {
			t.Fatalf("unlock: %d", unlocked.Status)
		}
		var cookie string
		for _, c := range (&http.Response{Header: unlocked.Header}).Cookies() {
			if strings.Contains(c.Name, "sh_pass_") {
				cookie = c.Name + "=" + c.Value
				if c.Domain != "" || !c.HttpOnly || !c.Secure || c.Path != "/" {
					t.Fatal("unlock cookie scope")
				}
			}
		}
		if cookie == "" {
			t.Fatal("unlock did not set cookie")
		}
		if r := a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": cookie}); r.Status != 200 {
			t.Fatalf("unlocked home: %d", r.Status)
		}
		if r := a.At(t, "GET", "home."+host, "/", nil, map[string]string{"Cookie": cookie}); r.Status != 401 {
			t.Fatal("unlock crossed to site host")
		}
		firstCookie = cookie

	}
}

func writeTestHomeFile(h *siteshttp.SiteHandler, id, name, content string) error {
	s, err := sitesrepo.GetSiteByID(context.Background(), h.Database, id)
	if err != nil {
		return err
	}
	p := filepath.Join(h.Disk.SiteDir(s.UserID, s.Name), "current", name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0644)
}
func requestForHomeTest(host string) *http.Request {
	r, _ := http.NewRequest("GET", "https://"+host+"/", nil)
	return r
}

func TestHomePendingCertificateCollisionIsolation(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	a.Sites.Hosts.SetSiteHosts("canonical", t.TempDir())
	owner := a.NewPerson(t, "pendinghome")
	a.Deploy(t, owner, "home")
	a.Deploy(t, owner, "other")
	uid, handle := a.UserID(t, owner)
	name := "home"
	if err := sitesrepo.SetHomeSite(context.Background(), a.Database, uid, &name); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/other/", nil, nil); r.Status != 503 || strings.Contains(string(r.Body), "<h1>other</h1>") {
		t.Fatalf("other site executed on home: %d", r.Status)
	}
	if r := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/home/", nil, nil); r.Status != 302 || r.Header.Get("Location") != "/" {
		t.Fatalf("home old path: %d %q", r.Status, r.Header.Get("Location"))
	}
}

func TestSmallBoxPathHomeAndCuration(t *testing.T) {
	a := servertest.NewPersonApp(t, "off")
	owner := a.NewPerson(t, "boxowner")
	a.Deploy(t, owner, "home")
	_, handle := a.UserID(t, owner)
	headers := map[string]string{"X-API-Key": owner.Key}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.Status != 200 {
		t.Fatalf("set: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/bio", map[string]any{"bio": "Small box"}, headers); r.Status != 200 {
		t.Fatalf("bio: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/home/showcase", map[string]any{"pinned": true, "order": 10}, headers); r.Status != 200 {
		t.Fatalf("pin: %d %s", r.Status, r.Body)
	}
	// Internal showcase serving is tested directly: the edge owns its access token.
	req := requestForHomeTest(servertest.PcContentHost)
	req.SetPathValue("handle", handle)
	recorder := httptest.NewRecorder()
	a.Sites.Showcase(recorder, req)
	if recorder.Code != 302 || recorder.Header().Get("Location") != a.Sites.SiteURL(handle, "home") {
		t.Fatalf("path home: %d %s", recorder.Code, recorder.Header().Get("Location"))
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/u/"+handle+"/showcase.json", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Small box") {
		t.Fatalf("feed: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, headers); r.Status != 200 {
		t.Fatalf("clear: %d", r.Status)
	}
	recorder = httptest.NewRecorder()
	a.Sites.Showcase(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("showcase fallback: %d", recorder.Code)
	}
}
