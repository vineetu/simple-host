package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

func TestHomeSettingServingAndIsolation(t *testing.T) {
	a := newPersonApp(t, "canonical")
	a.sites.SetSiteHosts("canonical", "")
	owner := a.newPerson(t, "homeowner")
	other := a.newPerson(t, "otherhome")
	a.deploy(t, owner, "home")
	a.deploy(t, owner, "blog")
	a.deploy(t, other, "foreign")
	uid, handle := a.userID(t, owner)
	_, oh := a.userID(t, other)
	host := handle + "." + pcSiteDomain
	headers := map[string]string{"X-API-Key": owner.key}
	for _, body := range []any{map[string]any{}, map[string]any{"site": 4}, map[string]any{"site": "../home"}} {
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", body, headers); r.status != 400 {
			t.Fatalf("invalid: %d", r.status)
		}
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "foreign"}, headers); r.status != 404 {
		t.Fatalf("foreign: %d", r.status)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, nil); r.status != 401 {
		t.Fatalf("anonymous: %d", r.status)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.status != 200 {
		t.Fatalf("set: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/me", nil, headers); r.json(t)["home_site"] != "home" {
		t.Fatalf("me: %s", r.body)
	}
	if r := a.at(t, "GET", host, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>home</h1>" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("home: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/sub/", nil, nil); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("sub: %d", r.status)
	}
	if r := a.at(t, "GET", "home."+host, "/", nil, nil); r.status != 200 {
		t.Fatalf("normal host: %d", r.status)
	}
	if r := a.at(t, "GET", host, "/blog/sub/?q=1", nil, nil); r.status != 302 || r.header.Get("Location") != "https://blog."+host+"/sub/?q=1" {
		t.Fatalf("collision: %d %s", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", host, "/missing", nil, nil); r.status != 404 {
		t.Fatalf("404: %d", r.status)
	}
	id := a.siteID(t, owner, "home")
	// A file wins over the old path link, and the site's own 404 is used.
	for name, content := range map[string]string{"blog/index.html": "home blog", "404.html": "home 404"} {
		if err := writeTestHomeFile(a.sites, id, name, content); err != nil {
			t.Fatal(err)
		}
	}
	if r := a.at(t, "GET", host, "/blog/", nil, nil); string(r.body) != "home blog" {
		t.Fatalf("file collision: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/missing", nil, nil); r.status != 404 || string(r.body) != "home 404" {
		t.Fatalf("own 404: %d %s", r.status, r.body)
	}

	if r := a.at(t, "GET", host, "/v1/sites/"+handle+"/me", nil, browser(host, "")); r.status != 200 {
		t.Fatalf("helper host alias: %d", r.status)
	}
	vic := a.newPerson(t, "homevisitor")
	cookie := a.session(t, vic, id, host)
	patch := map[string]any{"ops": []map[string]any{{"op": "set", "path": "hello", "value": 1}}}
	if r := a.at(t, "PATCH", host, "/v1/sites/home/state", patch, browser(host, cookie)); r.status != 200 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	for _, p := range []string{"/v1/sites/blog/state", "/v1/u/" + oh + "/sites/foreign/state"} {
		if r := a.at(t, "GET", host, p, nil, browser(host, cookie)); r.status == 200 {
			t.Fatalf("unscoped: %s", p)
		}
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/u/"+handle+"/sites/blog/state", nil, map[string]string{"Origin": "https://" + host}); r.status == 200 {
		t.Fatal("home origin reads blog on apex")
	}
	if r := a.at(t, "GET", host, "/v1/me", nil, browser(host, cookie)); r.status != 401 {
		t.Fatalf("visitor became owner: %d", r.status)
	}
	for _, crossHost := range []string{"home." + host, "blog." + host, pcSiteDomain} {
		req := requestForHomeTest(crossHost)
		sess := db.VisitorSession{Host: host, SiteID: id, ExpiresAt: time.Now().Add(time.Hour), IdleExpiresAt: time.Now().Add(time.Hour)}
		if a.sites.sessionValidFor(req, sess, id) {
			t.Fatal("session crossed host")
		}
	}
	otherCookie := a.session(t, vic, id, "home."+host)
	if r := a.at(t, "PATCH", host, "/v1/sites/home/state", patch, browser(host, otherCookie)); r.status == 200 {
		t.Fatal("site session accepted on home")
	}
	// Current storage resources save on the home origin with the same visitor cookie.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/home/storage/resources/form", map[string]any{"kind": "kv", "read": "anyone", "write": "signed-in"}, headers); r.status != 201 {
		t.Fatalf("resource: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", host, "/v1/sites/home/storage/kv/form/keys/message", map[string]any{"value": "saved on home"}, browser(host, cookie)); r.status != 200 {
		t.Fatalf("storage save: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/home/storage/kv/form/keys/message", nil, browser(host, cookie)); r.status != 200 || r.json(t)["value"] != "saved on home" {
		t.Fatalf("storage read: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/blog/storage/resources", nil, browser(host, cookie)); r.status == 200 {
		t.Fatal("storage crossed sites")
	}
	if id, ok := a.sites.PersonReturnSite(context.Background(), host, "/any/deep/path"); !ok || id != a.siteID(t, owner, "home") {
		t.Fatal("OAuth return did not bind home")
	}
	// A selected home does not inherit the site's normal-host session, nor another home site's cookie.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "blog"}, headers); r.status != 200 {
		t.Fatal("switch failed")
	}
	if r := a.at(t, "GET", host, "/v1/sites/blog/me", nil, browser(host, cookie)); r.json(t)["signed_in"] == true {
		t.Fatal("home session widened after switch")
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.status != 200 {
		t.Fatal("switch back failed")
	}
	// Deploy scope is enforced both in the route and in the global scope gate.
	deployKey, _ := auth.GenerateAPIKey()
	if _, err := a.database.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, db.HashAPIKey(deployKey), uid); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, map[string]string{"X-API-Key": deployKey}); r.status != 403 {
		t.Fatalf("deploy key: %d", r.status)
	}
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/home", map[string]any{"name": "renamedhome"}, headers); r.status != 200 {
		t.Fatalf("rename: %s", r.body)
	}
	if s, has, err := db.HomeSite(context.Background(), a.database, uid); err != nil || !has || s.ID != id || s.Name != "renamedhome" {
		t.Fatalf("rename lost home: %+v %v", s, err)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, headers); r.status != 200 {
		t.Fatalf("unset: %s", r.body)
	}
	if r := a.at(t, "GET", host, "/", nil, nil); !strings.Contains(string(r.body), "SHOWCASE") {
		t.Fatal("showcase not restored")
	}
}

func TestHomeFallbacks(t *testing.T) {
	for _, kind := range []string{"offline", "taken-down", "suspended", "recently-deleted", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			a := newPersonApp(t, "canonical")
			a.sites.SetSiteHosts("canonical", "")
			owner := a.newPerson(t, "fallback")
			a.deploy(t, owner, "home")
			uid, handle := a.userID(t, owner)
			name := "home"
			if err := db.SetHomeSite(context.Background(), a.database, uid, &name); err != nil {
				t.Fatal(err)
			}
			id := a.siteID(t, owner, "home")
			var err error
			switch kind {
			case "offline":
				err = db.SetSiteOffline(context.Background(), a.database, id, true)
			case "taken-down":
				err = db.SetSiteSuspended(context.Background(), a.database, id, "test")
			case "suspended":
				err = db.SetUserSuspended(context.Background(), a.database, uid, "test")
			case "recently-deleted":
				err = db.MarkSiteDeleted(context.Background(), a.database, id)
			case "deleted":
				_, err = a.database.Exec(`DELETE FROM sites WHERE id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "SHOWCASE") {
				t.Fatalf("fallback %s: %d", kind, r.status)
			}
			_, has, err := db.HomeSite(context.Background(), a.database, uid)
			want := kind != "recently-deleted" && kind != "deleted"
			if err != nil || has != want {
				t.Fatalf("setting retained=%v want=%v %v", has, want, err)
			}
		})
	}
}

func TestHomeBaseAndPasscode(t *testing.T) {
	a := newPersonApp(t, "canonical")
	a.sites.SetSiteHosts("canonical", "")
	owner := a.newPerson(t, "lockedhome")
	a.deploy(t, owner, "home")
	uid, handle := a.userID(t, owner)
	name := "home"
	if err := db.SetHomeSite(context.Background(), a.database, uid, &name); err != nil {
		t.Fatal(err)
	}
	a.sites.SetSiteBase("simple-host.alt", "serve", "")
	// Existing gate and generation cookies apply independently on the two hosts.
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-API-Key": owner.key}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/home/lock", map[string]any{"passcode": "123456"}, headers); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	var firstCookie string
	for _, base := range []string{pcSiteDomain, "simple-host.alt"} {
		host := handle + "." + base
		if r := a.at(t, "GET", host, "/", nil, nil); r.status != 401 {
			t.Fatalf("gate on %s: %d", host, r.status)
		}
		if r := a.at(t, "GET", host, "/v1/sites/home/state", nil, browser(host, "")); r.status != 403 {
			t.Fatalf("data gate: %d", r.status)
		}
		if firstCookie != "" {
			if r := a.at(t, "GET", host, "/", nil, map[string]string{"Cookie": firstCookie}); r.status != 401 {
				t.Fatal("unlock crossed base hosts")
			}
		}
		unlocked := a.at(t, "POST", host, "/v1/site-unlock", "passcode=123456&next=%2F", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"})
		if unlocked.status != 303 {
			t.Fatalf("unlock: %d", unlocked.status)
		}
		var cookie string
		for _, c := range (&http.Response{Header: unlocked.header}).Cookies() {
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
		if r := a.at(t, "GET", host, "/", nil, map[string]string{"Cookie": cookie}); r.status != 200 {
			t.Fatalf("unlocked home: %d", r.status)
		}
		if r := a.at(t, "GET", "home."+host, "/", nil, map[string]string{"Cookie": cookie}); r.status != 401 {
			t.Fatal("unlock crossed to site host")
		}
		firstCookie = cookie

	}
}

func writeTestHomeFile(h *SiteHandler, id, name, content string) error {
	s, err := db.GetSiteByID(context.Background(), h.database, id)
	if err != nil {
		return err
	}
	p := filepath.Join(h.disk.SiteDir(s.UserID, s.Name), "current", name)
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
	a := newPersonApp(t, "canonical")
	a.sites.SetSiteHosts("canonical", t.TempDir())
	owner := a.newPerson(t, "pendinghome")
	a.deploy(t, owner, "home")
	a.deploy(t, owner, "other")
	uid, handle := a.userID(t, owner)
	name := "home"
	if err := db.SetHomeSite(context.Background(), a.database, uid, &name); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/other/", nil, nil); r.status != 503 || strings.Contains(string(r.body), "<h1>other</h1>") {
		t.Fatalf("other site executed on home: %d", r.status)
	}
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/home/", nil, nil); r.status != 302 || r.header.Get("Location") != "/" {
		t.Fatalf("home old path: %d %q", r.status, r.header.Get("Location"))
	}
}

func TestSmallBoxPathHomeAndCuration(t *testing.T) {
	a := newPersonApp(t, "off")
	owner := a.newPerson(t, "boxowner")
	a.deploy(t, owner, "home")
	_, handle := a.userID(t, owner)
	headers := map[string]string{"X-API-Key": owner.key}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "home"}, headers); r.status != 200 {
		t.Fatalf("set: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/bio", map[string]any{"bio": "Small box"}, headers); r.status != 200 {
		t.Fatalf("bio: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/home/showcase", map[string]any{"pinned": true, "order": 10}, headers); r.status != 200 {
		t.Fatalf("pin: %d %s", r.status, r.body)
	}
	// Internal showcase serving is tested directly: the edge owns its access token.
	req := requestForHomeTest(pcContentHost)
	req.SetPathValue("handle", handle)
	recorder := httptest.NewRecorder()
	a.sites.showcase(recorder, req)
	if recorder.Code != 302 || recorder.Header().Get("Location") != a.sites.SiteURL(handle, "home") {
		t.Fatalf("path home: %d %s", recorder.Code, recorder.Header().Get("Location"))
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/u/"+handle+"/showcase.json", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Small box") {
		t.Fatalf("feed: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": nil}, headers); r.status != 200 {
		t.Fatalf("clear: %d", r.status)
	}
	recorder = httptest.NewRecorder()
	a.sites.showcase(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("showcase fallback: %d", recorder.Code)
	}
}
