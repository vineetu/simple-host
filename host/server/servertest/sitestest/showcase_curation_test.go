package sitestest

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestShowcasePinOrderAndBio(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	a.Sites.Hosts.SetSiteHosts("canonical", "")
	owner, other := a.NewPerson(t, "curator"), a.NewPerson(t, "othercurator")
	for _, name := range []string{"alpha", "beta", "gamma", "hidden"} {
		a.Deploy(t, owner, name)
	}
	uid, handle := a.UserID(t, owner)
	if _, err := a.Database.Exec(`UPDATE sites SET visibility=CASE WHEN name='hidden' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": owner.Key}
	set := func(name string, body any) {
		t.Helper()
		r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/"+name+"/showcase", body, key)
		if r.Status != 200 {
			t.Fatalf("preference: %d %s", r.Status, r.Body)
		}
	}
	set("alpha", map[string]any{"order": 5})
	set("beta", map[string]any{"pinned": true, "order": 3})
	set("gamma", map[string]any{"pinned": true, "order": 1})
	set("hidden", map[string]any{"pinned": true, "order": 0})
	pref := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/gamma/showcase", nil, key).JSON(t)
	if pref["pinned"] != true || pref["order"] != float64(1) {
		t.Fatalf("read preference: %v", pref)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"pinned": 2}, map[string]any{"order": -1}, map[string]any{"order": 1.5}, map[string]any{"order": 1000001}} {
		if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/gamma/showcase", bad, key); r.Status != 400 {
			t.Fatalf("invalid preference: %d", r.Status)
		}
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/gamma/showcase", map[string]any{"pinned": false}, map[string]string{"X-API-Key": other.Key}); r.Status != 404 {
		t.Fatalf("foreign preference: %d", r.Status)
	}
	bio := "My <projects> & adventures 🌴"
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/bio", map[string]any{"bio": bio}, key); r.Status != 200 || r.JSON(t)["bio"] != bio {
		t.Fatalf("bio: %d", r.Status)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/me/bio", nil, key); r.JSON(t)["max_length"] != float64(a.Sites.Limits.ShowcaseBioMaxLength) {
		t.Fatal("bio limit not reported")
	}
	data := a.At(t, "GET", servertest.PcSiteDomain, "/v1/u/"+handle+"/showcase.json", nil, nil).JSON(t)
	if data["bio"] != bio {
		t.Fatal("feed bio differs")
	}
	list := data["sites"].([]any)
	for i, name := range []string{"gamma", "beta", "alpha"} {
		if list[i].(map[string]any)["name"] != name {
			t.Fatalf("order: %v", list)
		}
	}
	if len(list) != 3 {
		t.Fatal("unlisted pin appeared")
	}
	html := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/", nil, nil)
	if strings.Contains(string(html.Body), `"bio":"My <projects>`) || !strings.Contains(string(html.Body), `My \u003cprojects\u003e`) {
		t.Fatal("bio not HTML-escaped in embedded JSON")
	}
	// List response supplies the dashboard values without a per-site fetch.
	sites := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, key)
	if !strings.Contains(string(sites.Body), `"pinned":true`) || !strings.Contains(string(sites.Body), `"order":5`) {
		t.Fatal("dashboard values missing")
	}
	// Changing one field preserves the other.
	set("gamma", map[string]any{"order": 4})
	pref = a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/gamma/showcase", nil, key).JSON(t)
	if pref["pinned"] != true || pref["order"] != float64(4) {
		t.Fatal("partial update lost pin")
	}
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/gamma", map[string]any{"name": "renamed"}, key); r.Status != 200 {
		t.Fatal("rename failed")
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.JSON(t)["pinned"] != true {
		t.Fatal("rename lost pin")
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/renamed", nil, key); r.Status != 204 {
		t.Fatal("delete failed")
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.Status != 404 {
		t.Fatal("deleted preferences still accessible")
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/renamed/restore", nil, key); r.Status != 200 {
		t.Fatal("restore failed")
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.JSON(t)["order"] != float64(4) {
		t.Fatal("restore lost order")
	}
	// Deployment-only credentials and visitor sessions cannot curate an account.
	dk, _ := auth.GenerateAPIKey()
	if _, err := a.Database.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, accountsrepo.HashAPIKey(dk), uid); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/me/bio", "/v1/sites/alpha/showcase"} {
		if r := a.At(t, "GET", servertest.PcSiteDomain, path, nil, map[string]string{"X-API-Key": dk}); r.Status != 403 {
			t.Fatalf("deploy read %s: %d", path, r.Status)
		}
		body := map[string]any{"bio": "No"}
		if strings.Contains(path, "/sites/") {
			body = map[string]any{"pinned": true}
		}
		if r := a.At(t, "PUT", servertest.PcSiteDomain, path, body, map[string]string{"X-API-Key": dk}); r.Status != 403 {
			t.Fatalf("deploy write %s: %d", path, r.Status)
		}
		if r := a.At(t, "PUT", servertest.PcSiteDomain, path, body, nil); r.Status != 401 {
			t.Fatalf("anonymous write %s: %d", path, r.Status)
		}
		if r := a.At(t, "GET", servertest.PcSiteDomain, path, nil, nil); r.Status != 401 {
			t.Fatalf("unauthorized %s: %d", path, r.Status)
		}
	}
	l := config.DefaultLimits()
	l.ShowcaseBioMaxLength = 3
	a.Sites.Limits = l
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴"}, key); r.Status != 200 {
		t.Fatal("unicode length rejected")
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴🌴"}, key); r.Status != 400 || r.JSON(t)["code"] != "bio_too_long" {
		t.Fatalf("limit: %d", r.Status)
	}
	if got, _ := accountsrepo.GetShowcaseBio(context.Background(), a.Database, uid); got != "🌴🌴🌴" {
		t.Fatal("rejected bio overwrote stored text")
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/me/bio", map[string]any{"bio": ""}, key); r.Status != 200 || r.JSON(t)["bio"] != "" {
		t.Fatal("bio clear failed")
	}
}
