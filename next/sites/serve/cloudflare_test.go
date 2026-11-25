package serve_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/data/values"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
)

// An open site's page is kept (a minute in browsers, an hour at the edge,
// tagged by the site's id); a locked site's page and a data route never are.
func TestCacheHeaders(t *testing.T) {
	k := testkit.Start(t, serve.Mount, values.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "blog", map[string]string{"index.html": "home"})
	k.Publish(t, bob, "diary", map[string]string{"index.html": "secret"})
	k.Exec(t, `UPDATE sites SET access = 'passcode' WHERE name = 'diary'`)
	blog, _ := k.App.Sites.Get(context.Background(), bob.ID, "blog")

	r := testkit.Call(t, b, "GET", "https://blog--bob.simple-host.site/", nil)
	if r.Status != 200 || r.Header.Get("Cache-Control") != "public, max-age=60" ||
		r.Header.Get("Cloudflare-CDN-Cache-Control") != "public, max-age=3600" || r.Header.Get("Cache-Tag") != "site:"+blog.ID {
		t.Fatalf("open page: %d %v", r.Status, r.Header)
	}
	for _, url := range []string{"https://diary--bob.simple-host.site/", "https://blog--bob.simple-host.site/v1/sites/blog/storage/kv/notes/keys"} {
		r := testkit.Call(t, b, "GET", url, nil)
		if r.Header.Get("Cache-Control") != "private, no-store" || r.Header.Get("Cache-Tag") != "" || r.Header.Get("Cloudflare-CDN-Cache-Control") != "" {
			t.Fatalf("%s: %d %v", url, r.Status, r.Header)
		}
	}
}

// Behind Cloudflare a publish and an access change each purge the site's
// tag once, and a Cloudflare failure does not fail the publish.
func TestPurgeAfterChange(t *testing.T) {
	k := testkit.Start(t, publish.Mount, api.Mount)
	k.BehindCloudflare()
	b := k.Browser()
	bob := k.Person(t, "bob")
	auth := []string{"Authorization", "Bearer " + bob.Key, "Content-Type", "application/json"}
	put := func() testkit.Res {
		return testkit.Call(t, b, "PUT", "https://simple-host.app/v1/sites/blog", map[string]any{"files": map[string]string{"index.html": "hi"}}, auth...)
	}
	if r := put(); r.Status != 200 || !strings.Contains(r.Body, "Visitors may see the previous version for up to a minute.") {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	site, _ := k.App.Sites.Get(context.Background(), bob.ID, "blog")
	purged := func(n int) {
		t.Helper()
		for i := 0; i < 100 && len(k.Cloudflare.Purges()) < n; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		p := k.Cloudflare.Purges()
		if len(p) != n || !slices.Contains(p[n-1], "site:"+site.ID) || !slices.Contains(p[n-1], "owner:"+bob.ID) {
			t.Fatalf("purges: %v, want %d with site:%s", p, n, site.ID)
		}
	}
	purged(1)
	if r := testkit.Call(t, b, "PATCH", "https://simple-host.app/v1/sites/blog", map[string]any{"access": "passcode", "passcode": "open sesame"}, auth...); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	purged(2)
	k.Cloudflare.Fail = true
	if r := put(); r.Status != 200 {
		t.Fatalf("publish while Cloudflare fails: %d %s", r.Status, r.Body)
	}
}

// With no Cloudflare settings nothing is called and the answer is as before.
func TestNoCloudflare(t *testing.T) {
	k := testkit.Start(t, publish.Mount, api.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	auth := []string{"Authorization", "Bearer " + bob.Key, "Content-Type", "application/json"}
	r := testkit.Call(t, b, "PUT", "https://simple-host.app/v1/sites/blog", map[string]any{"files": map[string]string{"index.html": "hi"}}, auth...)
	if r.Status != 200 || strings.Contains(r.Body, "note") {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	testkit.Call(t, b, "PATCH", "https://simple-host.app/v1/sites/blog", map[string]any{"access": "passcode", "passcode": "open sesame"}, auth...)
	time.Sleep(200 * time.Millisecond)
	if p := k.Cloudflare.Purges(); len(p) != 0 {
		t.Fatalf("Cloudflare called without settings: %v", p)
	}
}
