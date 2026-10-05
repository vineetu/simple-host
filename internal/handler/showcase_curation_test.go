package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

func TestNormalizeShowcaseBio(t *testing.T) {
	for _, x := range []struct {
		in, want string
		max      int
		bad      bool
	}{
		{"  My projects\nHello  ", "My projects\nHello", 40, false},
		{"<script>alert('x')</script>", "<script>alert('x')</script>", 40, false},
		{"🌴🌴🌴", "🌴🌴🌴", 3, false},
		{"🌴🌴🌴🌴", "", 3, true},
		{"", "", 3, false},
	} {
		got, err := normalizeBio(x.in, x.max)
		if (err != nil) != x.bad || got != x.want {
			t.Fatalf("normalize bio: %q %v", got, err)
		}
	}
}

func TestShowcasePinOrderAndBio(t *testing.T) {
	a := newPersonApp(t, "canonical")
	a.sites.SetSiteHosts("canonical", "")
	owner, other := a.newPerson(t, "curator"), a.newPerson(t, "othercurator")
	for _, name := range []string{"alpha", "beta", "gamma", "hidden"} {
		a.deploy(t, owner, name)
	}
	uid, handle := a.userID(t, owner)
	if _, err := a.database.Exec(`UPDATE sites SET visibility=CASE WHEN name='hidden' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": owner.key}
	set := func(name string, body any) {
		t.Helper()
		r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/"+name+"/showcase", body, key)
		if r.status != 200 {
			t.Fatalf("preference: %d %s", r.status, r.body)
		}
	}
	set("alpha", map[string]any{"order": 5})
	set("beta", map[string]any{"pinned": true, "order": 3})
	set("gamma", map[string]any{"pinned": true, "order": 1})
	set("hidden", map[string]any{"pinned": true, "order": 0})
	pref := a.at(t, "GET", pcSiteDomain, "/v1/sites/gamma/showcase", nil, key).json(t)
	if pref["pinned"] != true || pref["order"] != float64(1) {
		t.Fatalf("read preference: %v", pref)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"pinned": 2}, map[string]any{"order": -1}, map[string]any{"order": 1.5}, map[string]any{"order": 1000001}} {
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/gamma/showcase", bad, key); r.status != 400 {
			t.Fatalf("invalid preference: %d", r.status)
		}
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/gamma/showcase", map[string]any{"pinned": false}, map[string]string{"X-API-Key": other.key}); r.status != 404 {
		t.Fatalf("foreign preference: %d", r.status)
	}
	bio := "My <projects> & adventures 🌴"
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/bio", map[string]any{"bio": bio}, key); r.status != 200 || r.json(t)["bio"] != bio {
		t.Fatalf("bio: %d", r.status)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/me/bio", nil, key); r.json(t)["max_length"] != float64(config.Active().ShowcaseBioMaxLength) {
		t.Fatal("bio limit not reported")
	}
	data := a.at(t, "GET", pcSiteDomain, "/v1/u/"+handle+"/showcase.json", nil, nil).json(t)
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
	html := a.at(t, "GET", handle+"."+pcSiteDomain, "/", nil, nil)
	if strings.Contains(string(html.body), `"bio":"My <projects>`) || !strings.Contains(string(html.body), `My \u003cprojects\u003e`) {
		t.Fatal("bio not HTML-escaped in embedded JSON")
	}
	// List response supplies the dashboard values without a per-site fetch.
	sites := a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, key)
	if !strings.Contains(string(sites.body), `"pinned":true`) || !strings.Contains(string(sites.body), `"order":5`) {
		t.Fatal("dashboard values missing")
	}
	// Changing one field preserves the other.
	set("gamma", map[string]any{"order": 4})
	pref = a.at(t, "GET", pcSiteDomain, "/v1/sites/gamma/showcase", nil, key).json(t)
	if pref["pinned"] != true || pref["order"] != float64(4) {
		t.Fatal("partial update lost pin")
	}
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/gamma", map[string]any{"name": "renamed"}, key); r.status != 200 {
		t.Fatal("rename failed")
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.json(t)["pinned"] != true {
		t.Fatal("rename lost pin")
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/renamed", nil, key); r.status != 204 {
		t.Fatal("delete failed")
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.status != 404 {
		t.Fatal("deleted preferences still accessible")
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/renamed/restore", nil, key); r.status != 200 {
		t.Fatal("restore failed")
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/renamed/showcase", nil, key); r.json(t)["order"] != float64(4) {
		t.Fatal("restore lost order")
	}
	// Deployment-only credentials and visitor sessions cannot curate an account.
	dk, _ := auth.GenerateAPIKey()
	if _, err := a.database.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, db.HashAPIKey(dk), uid); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/me/bio", "/v1/sites/alpha/showcase"} {
		if r := a.at(t, "GET", pcSiteDomain, path, nil, map[string]string{"X-API-Key": dk}); r.status != 403 {
			t.Fatalf("deploy read %s: %d", path, r.status)
		}
		body := map[string]any{"bio": "No"}
		if strings.Contains(path, "/sites/") {
			body = map[string]any{"pinned": true}
		}
		if r := a.at(t, "PUT", pcSiteDomain, path, body, map[string]string{"X-API-Key": dk}); r.status != 403 {
			t.Fatalf("deploy write %s: %d", path, r.status)
		}
		if r := a.at(t, "PUT", pcSiteDomain, path, body, nil); r.status != 401 {
			t.Fatalf("anonymous write %s: %d", path, r.status)
		}
		if r := a.at(t, "GET", pcSiteDomain, path, nil, nil); r.status != 401 {
			t.Fatalf("unauthorized %s: %d", path, r.status)
		}
	}
	old := *config.Active()
	l := old
	l.ShowcaseBioMaxLength = 3
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴"}, key); r.status != 200 {
		t.Fatal("unicode length rejected")
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴🌴"}, key); r.status != 400 || r.json(t)["code"] != "bio_too_long" {
		t.Fatalf("limit: %d", r.status)
	}
	if got, _ := db.GetShowcaseBio(context.Background(), a.database, uid); got != "🌴🌴🌴" {
		t.Fatal("rejected bio overwrote stored text")
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/me/bio", map[string]any{"bio": ""}, key); r.status != 200 || r.json(t)["bio"] != "" {
		t.Fatal("bio clear failed")
	}
}
