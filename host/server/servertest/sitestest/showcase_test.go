package sitestest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/web"
)

// The public feed lists only public, servable sites, exactly as the
// showcase page embeds them, and nothing for a suspended owner.
func TestShowcaseFeedMatchesPublicProjection(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	owner := a.NewPerson(t, "feedowner")
	ids := map[string]string{}
	for _, name := range []string{"visible", "unlisted", "offline", "locked", "taken-down", "deleted"} {
		ids[name] = a.Deploy(t, owner, name)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET visibility = CASE WHEN name='unlisted' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	a.writeLive(t, owner, "visible", "index.html", `<!doctype html><title>My &amp; Projects</title><meta name="description" content="A plain &lt;description&gt;"><h1>Visible</h1>`)
	for _, err := range []error{
		sitesrepo.SetSiteOffline(ctx, a.Database, ids["offline"], true),
		sitesrepo.SetSiteSuspended(ctx, a.Database, ids["taken-down"], "test"),
		sitesrepo.MarkSiteDeleted(ctx, a.Database, ids["deleted"], 7*24*time.Hour),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	rt.SetPasscode(t, a.Database, ids["locked"])
	path := "/v1/u/" + owner.Handle + "/showcase.json"
	r := a.At(t, "GET", rt.SiteDomain, path, nil, map[string]string{"Origin": "https://other.test", "Cookie": "unused=1"})
	if r.Status != 200 || r.Header.Get("Access-Control-Allow-Origin") != "*" || r.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("feed response: %d %v", r.Status, r.Header)
	}
	var feed struct {
		Sites []web.ShowcaseSite `json:"sites"`
	}
	if err := json.Unmarshal(r.Body, &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Sites) != 1 {
		t.Fatalf("public visibility: %s", r.Body)
	}
	if s := feed.Sites[0]; s.Name != "visible" || s.Title != "My & Projects" || s.Description != "A plain <description>" || s.URL != "https://"+owner.SiteHost("visible")+"/" {
		t.Fatalf("entry: %+v", s)
	}
	html := a.At(t, "GET", owner.Handle+"."+rt.SiteDomain, "/", nil, nil)
	rawSites, _ := json.Marshal(feed.Sites)
	if !strings.Contains(string(html.Body), `"sites":`+string(rawSites)) {
		t.Fatal("HTML and feed diverged")
	}
	if err := accountsrepo.SetUserSuspended(ctx, a.Database, owner.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", rt.SiteDomain, path, nil, nil); r.Status != 200 || len(r.JSON(t)["sites"].([]any)) != 0 {
		t.Fatalf("suspended: %d %s", r.Status, r.Body)
	}
}

// The feed is rate limited per reader across hosts; an unknown handle is 404.
func TestShowcaseFeedReadLimit(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "feedlimit")
	a.Sites.ShowcaseLimiter = httpx.NewRateLimiter(1, 0)
	path := "/v1/u/" + owner.Handle + "/showcase.json"
	if r := a.At(t, "GET", rt.SiteDomain, path, nil, nil); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	if r := a.At(t, "GET", owner.Handle+"."+rt.SiteDomain, path, nil, nil); r.Status != 429 {
		t.Fatalf("rate across hosts: %d", r.Status)
	}
	a.Sites.ShowcaseLimiter = httpx.NewRateLimiter(100, 0)
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/u/not-a-person/showcase.json", nil, nil); r.Status != 404 {
		t.Fatalf("missing: %d", r.Status)
	}
}

// Pins and order shape the feed (unlisted sites stay out), preferences
// follow a rename and a restore, the bio is escaped in the page, and only
// the owner's full key may curate.
func TestShowcasePinOrderAndBio(t *testing.T) {
	a := NewApp(t)
	owner, other := a.NewPerson(t, "curator"), a.NewPerson(t, "othercurator")
	for _, name := range []string{"alpha", "beta", "gamma", "hidden"} {
		a.Deploy(t, owner, name)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET visibility=CASE WHEN name='hidden' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	set := func(name string, body any) {
		t.Helper()
		if r := a.api(t, owner, "PUT", "/v1/sites/"+name+"/showcase", body); r.Status != 200 {
			t.Fatalf("preference: %d %s", r.Status, r.Body)
		}
	}
	set("alpha", map[string]any{"order": 5})
	set("beta", map[string]any{"pinned": true, "order": 3})
	set("gamma", map[string]any{"pinned": true, "order": 1})
	set("hidden", map[string]any{"pinned": true, "order": 0})
	for _, bad := range []any{map[string]any{}, map[string]any{"order": -1}} {
		if r := a.api(t, owner, "PUT", "/v1/sites/gamma/showcase", bad); r.Status != 400 {
			t.Fatalf("invalid preference: %d", r.Status)
		}
	}
	if r := a.api(t, other, "PUT", "/v1/sites/gamma/showcase", map[string]any{"pinned": false}); r.Status != 404 {
		t.Fatalf("foreign preference: %d", r.Status)
	}
	bio := "My <projects> & adventures"
	if r := a.api(t, owner, "PUT", "/v1/me/bio", map[string]any{"bio": bio}); r.Status != 200 {
		t.Fatalf("bio: %d", r.Status)
	}
	data := a.At(t, "GET", rt.SiteDomain, "/v1/u/"+owner.Handle+"/showcase.json", nil, nil).JSON(t)
	list := data["sites"].([]any)
	if data["bio"] != bio || len(list) != 3 {
		t.Fatalf("feed: %v", data)
	}
	for i, name := range []string{"gamma", "beta", "alpha"} {
		if list[i].(map[string]any)["name"] != name {
			t.Fatalf("order: %v", list)
		}
	}
	if html := a.At(t, "GET", owner.Handle+"."+rt.SiteDomain, "/", nil, nil); strings.Contains(string(html.Body), `"bio":"My <projects>`) {
		t.Fatal("bio not HTML-escaped in embedded JSON")
	}
	// A partial update keeps the other field; rename and delete/restore keep both.
	set("gamma", map[string]any{"order": 4})
	if r := a.api(t, owner, "PATCH", "/v1/sites/gamma", map[string]any{"name": "renamed"}); r.Status != 200 {
		t.Fatal("rename failed")
	}
	if r := a.api(t, owner, "DELETE", "/v1/sites/renamed", nil); r.Status != 204 {
		t.Fatal("delete failed")
	}
	if r := a.api(t, owner, "GET", "/v1/sites/renamed/showcase", nil); r.Status != 404 {
		t.Fatal("deleted preferences still accessible")
	}
	if r := a.api(t, owner, "POST", "/v1/sites/renamed/restore", nil); r.Status != 200 {
		t.Fatal("restore failed")
	}
	if pref := a.api(t, owner, "GET", "/v1/sites/renamed/showcase", nil).JSON(t); pref["pinned"] != true || pref["order"] != float64(4) {
		t.Fatalf("preferences lost: %v", pref)
	}
	dk := a.deployKey(t, owner)
	for _, path := range []string{"/v1/me/bio", "/v1/sites/alpha/showcase"} {
		if r := a.At(t, "PUT", rt.SiteDomain, path, map[string]any{"bio": "No", "pinned": true}, dk); r.Status != 403 {
			t.Fatalf("deploy write %s: %d", path, r.Status)
		}
		if r := a.At(t, "PUT", rt.SiteDomain, path, map[string]any{"bio": "No", "pinned": true}, nil); r.Status != 401 {
			t.Fatalf("anonymous write %s: %d", path, r.Status)
		}
	}
	// The bio limit counts characters, and a refused bio leaves the old one.
	a.Sites.Limits.ShowcaseBioMaxLength = 3
	if r := a.api(t, owner, "PUT", "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴"}); r.Status != 200 {
		t.Fatal("unicode length rejected")
	}
	if r := a.api(t, owner, "PUT", "/v1/me/bio", map[string]any{"bio": "🌴🌴🌴🌴"}); r.Status != 400 || r.Code() != "bio_too_long" {
		t.Fatalf("limit: %d", r.Status)
	}
	if got, _ := accountsrepo.GetShowcaseBio(context.Background(), a.Database, owner.ID); got != "🌴🌴🌴" {
		t.Fatal("rejected bio overwrote stored text")
	}
}
