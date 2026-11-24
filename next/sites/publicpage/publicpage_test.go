package publicpage_test

import (
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/publicpage"
	"simplehost/sites/serve"
)

// SPEC test 31 (F22): the public page lists only switched-on sites. The
// sitemap leaves out locked and offline sites.
func TestPublicPageAndSitemap(t *testing.T) {
	k := testkit.Start(t, serve.Mount, publicpage.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	for _, name := range []string{"listed", "unlisted", "locked", "away"} {
		k.Publish(t, bob, name, map[string]string{"index.html": name + " home", "style.css": "x"})
	}
	k.Exec(t, `UPDATE sites SET listed = true WHERE name IN ('listed', 'locked', 'away')`)
	k.Exec(t, `UPDATE sites SET access = 'passcode' WHERE name = 'locked'`)
	k.Exec(t, `UPDATE sites SET offline = true WHERE name = 'away'`)

	page := testkit.Call(t, b, "GET", "https://bob.simple-host.site/", nil)
	if page.Status != 200 || !strings.Contains(page.Body, "https://listed--bob.simple-host.site/") || !strings.Contains(page.Body, "locked--bob") ||
		strings.Contains(page.Body, "unlisted--bob") || strings.Contains(page.Body, "away--bob") {
		t.Fatalf("public page: %d %s", page.Status, page.Body)
	}
	sm := testkit.Call(t, b, "GET", "https://bob.simple-host.site/sitemap.xml", nil)
	if sm.Status != 200 || !strings.Contains(sm.Body, "<loc>https://listed--bob.simple-host.site/</loc>") || !strings.Contains(sm.Body, "unlisted--bob") ||
		strings.Contains(sm.Body, "locked--bob") || strings.Contains(sm.Body, "away--bob") {
		t.Fatalf("sitemap: %d %s", sm.Status, sm.Body)
	}
	// One of the sites can be the page instead, every path of it.
	k.Exec(t, `INSERT INTO owner_addresses (owner_id, home_site_id) SELECT owner_id, id FROM sites WHERE name = 'listed'`)
	for path, want := range map[string]string{"": "listed home", "style.css": "x"} {
		if r := testkit.Call(t, b, "GET", "https://bob.simple-host.site/"+path, nil); r.Status != 200 || r.Body != want {
			t.Fatalf("home site /%s: %d %q", path, r.Status, r.Body)
		}
	}
}
