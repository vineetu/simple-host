package sitestest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/web"
)

func TestShowcaseFeedMatchesPublicProjection(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	a.Sites.Hosts.SetSiteHosts("canonical", "")
	owner := a.NewPerson(t, "feedowner")
	uid, handle := a.UserID(t, owner)
	for _, name := range []string{"visible", "unlisted", "offline", "locked", "taken-down", "deleted"} {
		a.Deploy(t, owner, name)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET visibility = CASE WHEN name='unlisted' THEN 'unlisted' ELSE 'public' END WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if err := writeTestHomeFile(a.Sites, a.SiteID(t, owner, "visible"), "index.html", `<!doctype html><title>My &amp; Projects</title><meta name="description" content="A plain &lt;description&gt;"><h1>Visible</h1>`); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.SetSiteOffline(context.Background(), a.Database, a.SiteID(t, owner, "offline"), true); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.SetSiteSuspended(context.Background(), a.Database, a.SiteID(t, owner, "taken-down"), "test"); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.MarkSiteDeleted(context.Background(), a.Database, a.SiteID(t, owner, "deleted"), 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET passcode_enc='x'::bytea WHERE user_id=$1 AND name='locked'`, uid); err != nil {
		t.Fatal(err)
	}
	path := "/v1/u/" + handle + "/showcase.json"
	r := a.At(t, "GET", servertest.PcSiteDomain, path, nil, map[string]string{"Origin": "https://other.test", "Cookie": "unused=1"})
	if r.Status != 200 || r.Header.Get("Access-Control-Allow-Origin") != "*" || r.Header.Get("Access-Control-Allow-Credentials") != "" || r.Header.Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("feed response: %d", r.Status)
	}
	var feed struct {
		Handle string             `json:"handle"`
		Bio    string             `json:"bio"`
		Sites  []web.ShowcaseSite `json:"sites"`
	}
	if err := json.Unmarshal(r.Body, &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Handle != handle || feed.Bio != "" || len(feed.Sites) != 1 {
		t.Fatalf("public visibility: %s", r.Body)
	}
	s := feed.Sites[0]
	if s.Name != "visible" || s.Title != "My & Projects" || s.Description != "A plain <description>" || s.URL != "https://visible."+handle+"."+servertest.PcSiteDomain+"/" || s.UpdatedAt.IsZero() || s.Pinned || s.Order != 0 {
		t.Fatalf("entry: %+v", s)
	}
	// The server embeds exactly the same public site projection in HTML.
	html := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/", nil, nil)
	rawSites, _ := json.Marshal(feed.Sites)
	if !strings.Contains(string(html.Body), `"sites":`+string(rawSites)) {
		t.Fatal("HTML and feed diverged")
	}
	for _, name := range []string{"unlisted", "offline", "locked", "taken-down", "deleted"} {
		if strings.Contains(string(r.Body), `"name":"`+name+`"`) {
			t.Fatalf("hidden site: %s", name)
		}
	}
	// Choosing a custom home does not change the public feed.
	name := "visible"
	if err := sitesrepo.SetHomeSite(context.Background(), a.Database, uid, &name); err != nil {
		t.Fatal(err)
	}
	if again := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, path, nil, nil); string(again.Body) != string(r.Body) {
		t.Fatal("home changed feed")
	}
	// A suspended owner has no public entries, just as on the showcase.
	if err := accountsrepo.SetUserSuspended(context.Background(), a.Database, uid, "test"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, path, nil, nil); r.Status != 200 || len(r.JSON(t)["sites"].([]any)) != 0 {
		t.Fatalf("suspended: %d", r.Status)
	}
}

func TestShowcaseFeedReadLimit(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	owner := a.NewPerson(t, "feedlimit")
	_, handle := a.UserID(t, owner)
	a.Sites.ShowcaseLimiter = httpx.NewRateLimiter(1, 0)
	path := "/v1/u/" + handle + "/showcase.json"
	if r := a.At(t, "GET", servertest.PcSiteDomain, path, nil, nil); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	if r := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, path, nil, nil); r.Status != 429 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("rate across hosts: %d", r.Status)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/u/not-a-person/showcase.json", nil, nil); r.Status != 404 {
		t.Fatalf("missing: %d", r.Status)
	}
}
