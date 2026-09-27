package analytics

import (
	"strings"
	"testing"
)

func TestAttributeHosts(t *testing.T) {
	i := NewIngester(nil, "", "s", "sites.example.app", "example.app")
	m := &attrMaps{
		handleToUser: map[string]string{"olive": "u1"},
		userNameToID: map[string]string{"u1/shop": "s-shop", "u1/blog": "s-blog"},
		nameToOldest: map[string]string{"legacy": "s-legacy", "shop": "s-other-shop"},
		domainToID:   map[string]string{"clay.example.app": "s-clay", "brand.com": "s-brand"},
	}
	for _, tc := range []struct{ host, uri, want, path string }{
		{"sites.example.app", "/olive/shop/", "s-shop", "/"},
		{"sites.example.app", "/olive/shop/menu/drinks.html", "s-shop", "/menu/drinks.html"},
		{"sites.example.app", "/olive/shop", "s-shop", "/"},
		{"olive.example.app", "/shop/", "s-shop", "/"},
		{"olive.example.app", "/blog/post.html?x=1", "s-blog", "/post.html"},
		{"olive.example.app", "/", "", ""},
		{"olive.example.app", "/nope/", "", ""},
		{"clay.example.app", "/", "s-clay", "/"},
		{"legacy.example.app", "/", "s-legacy", "/"},
		{"brand.com", "/about/", "s-brand", "/about/"},
		{"shop.olive.example.app", "/", "s-shop", "/"},
		{"blog.olive.example.app", "/post.html?x=1", "s-blog", "/post.html"},
		{"nope.olive.example.app", "/", "", ""},
		{"shop.nobody.example.app", "/", "", ""},
		{"a.b.c.example.app", "/", "", ""},
	} {
		got, path := i.attribute(tc.host, tc.uri, m)
		if got != tc.want || (got != "" && path != tc.path) {
			t.Errorf("%s%s: got %q %q want %q %q", tc.host, tc.uri, got, path, tc.want, tc.path)
		}
	}
}

// A referrer is kept as its host name only, whatever shape it arrives in.
func TestReferrerDomain(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"-":                                    "",
		"news.ycombinator.com":                 "news.ycombinator.com",
		"WWW.Google.COM":                       "www.google.com",
		"https://t.co/abc?x=me@example.com":    "t.co",
		"https://user:pw@evil.example:8443/p":  "evil.example",
		"android-app://com.slack/":             "com.slack",
		"localhost":                            "",
		"not a host.com":                       "",
		"https://xn--bcher-kva.example/path#f": "xn--bcher-kva.example",
	} {
		if got := referrerDomain(in); got != want {
			t.Errorf("referrerDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPagePath(t *testing.T) {
	for in, want := range map[string]string{
		"/":                "/",
		"/index.html":      "/",
		"/docs/index.html": "/docs/",
		"/menu?utm=x":      "/menu",
		"about.html":       "/about.html",
	} {
		if got := pagePath(in); got != want {
			t.Errorf("pagePath(%q) = %q, want %q", in, got, want)
		}
	}
	long := "/" + strings.Repeat("a", 300)
	if got := pagePath(long); len(got) != maxPagePath {
		t.Errorf("long path kept %d bytes", len(got))
	}
}

// Old seven-field lines and new eight-field lines both parse; only the new
// ones carry a referrer.
func TestParseTSVBothFormats(t *testing.T) {
	old := "2026-09-27T10:00:00+00:00\tbrand.com\t200\tGET\t/\t203.0.113.9\tMozilla/5.0"
	l, ok := parseTSV(old)
	if !ok || l.ua != "Mozilla/5.0" || l.referrer != "" {
		t.Fatalf("seven fields: %+v %v", l, ok)
	}
	l, ok = parseTSV(old + "\tnews.ycombinator.com")
	if !ok || l.referrer != "news.ycombinator.com" {
		t.Fatalf("eight fields: %+v %v", l, ok)
	}
	l, ok = parseTSV(old + "\t-")
	if !ok || l.referrer != "" {
		t.Fatalf("eight fields, no referrer: %+v %v", l, ok)
	}
}
