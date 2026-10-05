package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

func TestShowcaseFeedMatchesPublicProjection(t *testing.T) {
	a := newPersonApp(t, "canonical")
	a.sites.SetSiteHosts("canonical", "")
	owner := a.newPerson(t, "feedowner")
	uid, handle := a.userID(t, owner)
	for _, name := range []string{"visible", "unlisted", "offline", "locked", "taken-down", "deleted"} {
		a.deploy(t, owner, name)
	}
	if _, err := a.database.Exec(`UPDATE sites SET visibility = CASE WHEN name='unlisted' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if err := writeTestHomeFile(a.sites, a.siteID(t, owner, "visible"), "index.html", `<!doctype html><title>My &amp; Projects</title><meta name="description" content="A plain &lt;description&gt;"><h1>Visible</h1>`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSiteOffline(context.Background(), a.database, a.siteID(t, owner, "offline"), true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSiteSuspended(context.Background(), a.database, a.siteID(t, owner, "taken-down"), "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkSiteDeleted(context.Background(), a.database, a.siteID(t, owner, "deleted")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET passcode_enc='x'::bytea WHERE user_id=$1 AND name='locked'`, uid); err != nil {
		t.Fatal(err)
	}
	path := "/v1/u/" + handle + "/showcase.json"
	r := a.at(t, "GET", pcSiteDomain, path, nil, map[string]string{"Origin": "https://other.test", "Cookie": "unused=1"})
	if r.status != 200 || r.header.Get("Access-Control-Allow-Origin") != "*" || r.header.Get("Access-Control-Allow-Credentials") != "" || r.header.Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("feed response: %d", r.status)
	}
	var feed struct {
		Handle string         `json:"handle"`
		Bio    string         `json:"bio"`
		Sites  []showcaseSite `json:"sites"`
	}
	if err := json.Unmarshal(r.body, &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Handle != handle || feed.Bio != "" || len(feed.Sites) != 1 {
		t.Fatalf("public visibility: %s", r.body)
	}
	s := feed.Sites[0]
	if s.Name != "visible" || s.Title != "My & Projects" || s.Description != "A plain <description>" || s.URL != "https://visible."+handle+"."+pcSiteDomain+"/" || s.UpdatedAt.IsZero() || s.Pinned || s.Order != 0 {
		t.Fatalf("entry: %+v", s)
	}
	// The server embeds exactly the same public site projection in HTML.
	html := a.at(t, "GET", handle+"."+pcSiteDomain, "/", nil, nil)
	rawSites, _ := json.Marshal(feed.Sites)
	if !strings.Contains(string(html.body), `"sites":`+string(rawSites)) {
		t.Fatal("HTML and feed diverged")
	}
	for _, name := range []string{"unlisted", "offline", "locked", "taken-down", "deleted"} {
		if strings.Contains(string(r.body), `"name":"`+name+`"`) {
			t.Fatalf("hidden site: %s", name)
		}
	}
	// Choosing a custom home does not change the public feed.
	name := "visible"
	if err := db.SetHomeSite(context.Background(), a.database, uid, &name); err != nil {
		t.Fatal(err)
	}
	if again := a.at(t, "GET", handle+"."+pcSiteDomain, path, nil, nil); string(again.body) != string(r.body) {
		t.Fatal("home changed feed")
	}
	// A suspended owner has no public entries, just as on the showcase.
	if err := db.SetUserSuspended(context.Background(), a.database, uid, "test"); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", pcSiteDomain, path, nil, nil); r.status != 200 || len(r.json(t)["sites"].([]any)) != 0 {
		t.Fatalf("suspended: %d", r.status)
	}
}

func TestShowcaseFeedReadLimit(t *testing.T) {
	a := newPersonApp(t, "canonical")
	owner := a.newPerson(t, "feedlimit")
	_, handle := a.userID(t, owner)
	a.sites.readLimiter = newRateLimiter(1, 0)
	path := "/v1/u/" + handle + "/showcase.json"
	if r := a.at(t, "GET", pcSiteDomain, path, nil, nil); r.status != 200 {
		t.Fatalf("first: %d", r.status)
	}
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, path, nil, nil); r.status != 429 || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("rate across hosts: %d", r.status)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/u/not-a-person/showcase.json", nil, nil); r.status != 404 {
		t.Fatalf("missing: %d", r.status)
	}
}
