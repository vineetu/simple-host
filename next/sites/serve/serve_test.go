package serve_test

import (
	"context"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/stats"
)

// SPEC test 14 (F10): a _redirects line sends 301. A bad line is reported
// with its line number. /about works three ways.
func TestRedirectsAndCleanLinks(t *testing.T) {
	k := testkit.Start(t, publish.Mount, serve.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	publish := func(files map[string]string) testkit.Res {
		return testkit.Call(t, b, "PUT", "https://simple-host.app/v1/sites/blog", map[string]any{"files": files},
			"X-API-Key", bob.Key, "Content-Type", "application/json")
	}
	if r := publish(map[string]string{"index.html": "home", "about.html": "about us", "new.html": "new",
		"_redirects": "# moved\n/old /new 301\n/gone https://example.com/ 302\n"}); r.Status != 200 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	site := "https://blog--bob.simple-host.site"
	if r := testkit.Call(t, b, "GET", site+"/old?x=1", nil); r.Status != 301 || r.Header.Get("Location") != "/new?x=1" {
		t.Fatalf("/old: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := testkit.Call(t, b, "GET", site+"/gone", nil); r.Status != 302 || r.Header.Get("Location") != "https://example.com/" {
		t.Fatalf("/gone: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := testkit.Call(t, b, "GET", site+"/_redirects", nil); r.Status != 404 {
		t.Fatalf("/_redirects is served: %d", r.Status)
	}
	r := publish(map[string]string{"index.html": "home", "_redirects": "/a /b\n/c\n"})
	if r.Status != 400 || !strings.Contains(r.Body, "line 2") {
		t.Fatalf("bad line: %d %s", r.Status, r.Body)
	}
	for _, path := range []string{"/about", "/about.html"} {
		if r := testkit.Call(t, b, "GET", site+path, nil); r.Status != 200 || r.Body != "about us" {
			t.Fatalf("%s: %d %q", path, r.Status, r.Body)
		}
	}
	if r := testkit.Call(t, b, "GET", site+"/about/", nil); r.Status != 301 || r.Header.Get("Location") != "/about" {
		t.Fatalf("/about/: %d %q", r.Status, r.Header.Get("Location"))
	}
}

// SPEC test 30 (F21): visits count once. The owner's own visits and bots are
// not counted as people.
func TestVisitsCountOnce(t *testing.T) {
	k := testkit.Start(t, publish.Mount, serve.Mount, stats.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "blog", map[string]string{"index.html": "home", "style.css": "x"})
	site := "https://blog--bob.simple-host.site/"
	browser := "Mozilla/5.0 (Macintosh) Safari/605.1.15"
	testkit.Call(t, b, "GET", site, nil, "User-Agent", browser, "Referer", "https://news.example.org/item")
	testkit.Call(t, b, "GET", site, nil, "User-Agent", browser)
	testkit.Call(t, b, "GET", site+"style.css", nil, "User-Agent", browser)                       // not a page
	testkit.Call(t, b, "GET", site, nil, "User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1)") // a bot
	testkit.Call(t, b, "GET", site, nil, "User-Agent", browser, "X-API-Key", bob.Key, "X-Real-IP", "10.0.0.9")
	serve.Flush(context.Background(), k.App)

	r := testkit.Call(t, b, "GET", "https://simple-host.app/v1/sites/blog/stats", nil, "X-API-Key", bob.Key)
	for _, want := range []string{`"views":2,"people":1,"bots":1`, `"pages":[{"key":"/","n":2}]`, `"referrers":[{"key":"news.example.org","n":1}]`} {
		if r.Status != 200 || !strings.Contains(r.Body, want) {
			t.Fatalf("stats: %d %s, want %s", r.Status, r.Body, want)
		}
	}
	olga := k.Person(t, "olga")
	if r := testkit.Call(t, b, "GET", "https://simple-host.app/v1/sites/blog/stats?owner=bob", nil, "X-API-Key", olga.Key); r.Status != 403 {
		t.Fatalf("someone else's numbers: %d %s", r.Status, r.Body)
	}
}
