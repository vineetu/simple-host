package sites_test

import (
	"context"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/publicpage"
	"simplehost/sites/serve"
)

// SPEC test 15 (F11): a new account's site answers at once on
// site--handle.simple-host.site, an existing one on site.handle.simple-host.app,
// and the address the service returns is the one that works.
func TestSiteAnswersAtItsAddress(t *testing.T) {
	k := testkit.Start(t, serve.Mount, publicpage.Mount)
	b := k.Browser()
	nina := k.Person(t, "nina")
	olga := k.Person(t, "olga")
	k.Exec(t, `INSERT INTO owner_addresses (owner_id, app_shape) VALUES ($1, true)`, olga.ID)

	k.Publish(t, nina, "blog", map[string]string{"index.html": "one"})
	k.Publish(t, olga, "shop", map[string]string{"index.html": "one"})
	for url, want := range map[string]string{
		"https://blog--nina.simple-host.site/": "one",
		"https://shop.olga.simple-host.app/":   "one",
	} {
		if r := testkit.Call(t, b, "GET", url, nil); r.Status != 200 || r.Body != want {
			t.Fatalf("%s: %d %q, want %q", url, r.Status, r.Body, want)
		}
	}
	// A new version and a new site answer at once, though the host was seen before.
	k.Publish(t, nina, "blog", map[string]string{"index.html": "two"})
	k.Publish(t, nina, "notes", map[string]string{"index.html": "one"})
	for url, want := range map[string]string{
		"https://blog--nina.simple-host.site/":  "two",
		"https://notes--nina.simple-host.site/": "one",
	} {
		if r := testkit.Call(t, b, "GET", url, nil); r.Status != 200 || r.Body != want {
			t.Fatalf("%s: %d %q, want %q", url, r.Status, r.Body, want)
		}
	}
	// The address a tool returns is the one that works, and the other shape does not.
	for who, name := range map[string]string{nina.ID: "blog", olga.ID: "shop"} {
		s, err := k.App.Sites.Get(context.Background(), who, name)
		if err != nil {
			t.Fatal(err)
		}
		if r := testkit.Call(t, b, "GET", k.App.Sites.URL(s), nil); r.Status != 200 {
			t.Fatalf("%s: %d", k.App.Sites.URL(s), r.Status)
		}
	}
	for _, url := range []string{"https://blog.nina.simple-host.app/", "https://shop--olga.simple-host.site/"} {
		if r := testkit.Call(t, b, "GET", url, nil); r.Status != 404 {
			t.Fatalf("%s: %d, want 404 (no redirects between shapes)", url, r.Status)
		}
	}
}

// SPEC test 16 (F11): a path-style old link answers "not found".
func TestPathStyleLinksAreNotFound(t *testing.T) {
	k := testkit.Start(t, serve.Mount, publicpage.Mount)
	b := k.Browser()
	nina := k.Person(t, "nina")
	olga := k.Person(t, "olga")
	k.Exec(t, `INSERT INTO owner_addresses (owner_id, app_shape) VALUES ($1, true)`, olga.ID)
	k.Publish(t, nina, "blog", map[string]string{"index.html": "x"})
	k.Publish(t, olga, "shop", map[string]string{"index.html": "x"})

	// The public pages themselves answer...
	for _, url := range []string{"https://nina.simple-host.site/", "https://olga.simple-host.app/"} {
		if r := testkit.Call(t, b, "GET", url, nil); r.Status != 200 {
			t.Fatalf("%s: %d", url, r.Status)
		}
	}
	// ...but no old path-style link reaches a site.
	for _, url := range []string{
		"https://sites.simple-host.app/olga/shop/",
		"https://sites.simple-host.site/nina/blog/",
		"https://olga.simple-host.app/shop/",
		"https://nina.simple-host.site/blog/",
		"https://simple-host.app/v1/u/olga/shop/",
	} {
		if r := testkit.Call(t, b, "GET", url, nil); r.Status != 404 {
			t.Fatalf("%s: %d %q, want 404", url, r.Status, r.Body)
		}
	}
}
