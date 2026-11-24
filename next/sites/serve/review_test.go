package serve_test

import (
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/serve"
)

// Review A2: the platform answers a published file as data, never as a page.
func TestPlatformReadsFilesAsData(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	m := k.Person(t, "mallory")
	k.Publish(t, m, "x", map[string]string{"evil.html": "<script>fetch('/v1/me')</script>", "index.html": "hi"})
	r := testkit.Call(t, k.Browser(), "GET", "https://simple-host.app/v1/sites/x/files/evil.html?owner=mallory", nil)
	if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") || r.Header.Get("X-Frame-Options") != "DENY" || r.Header.Get("Referrer-Policy") == "" {
		t.Fatalf("platform file: %d %v", r.Status, r.Header)
	}
}

// Coordinator C: a site's pages and files do not count against the
// per-address request limit; API calls still do.
func TestPageViewsAreNotCounted(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	m := k.Person(t, "olive")
	k.Publish(t, m, "shop", map[string]string{"index.html": "hi"})
	b := k.Browser()
	ip := []string{"X-Real-IP", "198.51.100.9"}
	for i := 0; i < 610; i++ {
		if r := testkit.Call(t, b, "GET", "https://shop--olive.simple-host.site/", nil, ip...); r.Status != 200 {
			t.Fatalf("page view %d: %d %s", i, r.Status, r.Body)
		}
	}
	limited := false
	for i := 0; i < 610 && !limited; i++ {
		limited = testkit.Call(t, b, "GET", "https://shop--olive.simple-host.site/v1/sites/shop/storage/kv/x/keys", nil, ip...).Status == 429
	}
	if !limited {
		t.Fatal("API calls on the site address are not limited")
	}
}
