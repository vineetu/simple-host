package journeys_test

import (
	"strings"
	"testing"

	"simplehost/core/testkit"
)

// An address of the service before it was rebuilt says so, and what to do.
func TestChangedAddresses(t *testing.T) {
	k := testkit.Start(t, all...)
	b := k.Browser()
	for _, c := range []struct {
		method, path string
		status       int
		has          string
	}{
		{"GET", "/v1/me/keys", 404, platform + "/llms.txt"},
		{"POST", "/v1/me", 405, platform + "/skills.zip"},
		{"POST", "/v1/sites/blog", 405, platform + "/llms.txt"},
	} {
		r := testkit.Call(t, b, c.method, platform+c.path, map[string]any{})
		if r.Status != c.status || !strings.Contains(r.Body, `"code":"changed"`) || !strings.Contains(r.Body, "rebuilt") || !strings.Contains(r.Body, c.has) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}

// The main domain tells crawlers where its public pages are.
func TestRobotsAndSitemap(t *testing.T) {
	k := testkit.Start(t, all...)
	b := k.Browser()
	if r := testkit.Call(t, b, "GET", platform+"/robots.txt", nil); r.Status != 200 || !strings.Contains(r.Body, "Sitemap: "+platform+"/sitemap.xml") {
		t.Fatalf("robots.txt: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", platform+"/sitemap.xml", nil); r.Status != 200 || !strings.Contains(r.Body, "<loc>"+platform+"/docs</loc>") {
		t.Fatalf("sitemap.xml: %d %s", r.Status, r.Body)
	}
}
