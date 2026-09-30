package analytics

import (
	"fmt"
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
		// One page, many spellings: decoded once, slashes collapsed, query
		// and fragment dropped, trailing slash kept, case kept.
		"/%61bout/":      "/about/",
		"//about/":       "/about/",
		"/about/?":       "/about/",
		"/about/#top":    "/about/",
		"/a//b///c":      "/a/b/c",
		"/x/../about/":   "/about/",
		"/About":         "/About",
		"/%2561bout":     "/%61bout",
		"/menu%3Fid=1":   "/menu",
		"/%69ndex.html":  "/",
		"/bad%zzescape/": "/bad%zzescape/",
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

// A site keeps at most max distinct items a day; new ones beyond that are
// counted as (other), stored ones keep counting, and (other) takes no slot.
func TestCapItems(t *testing.T) {
	have := map[string]bool{"/a": true, "/b": true, otherItem: true}
	got := capItems(have, map[string]int64{"/a": 1, "/c": 5, "/d": 3, "/e": 1, otherItem: 2}, 3)
	want := map[string]int64{"/a": 1, "/c": 5, otherItem: 2 + 3 + 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("capItems = %v, want %v", got, want)
	}
	if got := capItems(map[string]bool{}, map[string]int64{"/x": 1}, 3); got["/x"] != 1 || len(got) != 1 {
		t.Errorf("under the cap = %v", got)
	}
}

// Under a moving base (SITE_BASE_DOMAIN) hits on either domain attribute to
// the same sites, including a claimed name stored under the other one.
func TestAttributeBothBases(t *testing.T) {
	i := NewIngester(nil, "", "s", "sites.example.app", "example.app").WithBases("example.site", "EXAMPLE.app", "")
	if len(i.alsoBases) != 1 {
		t.Fatalf("bases: %v", i.alsoBases)
	}
	m := &attrMaps{
		handleToUser: map[string]string{"olive": "u1"},
		userNameToID: map[string]string{"u1/shop": "s-shop"},
		nameToOldest: map[string]string{"legacy": "s-legacy"},
		domainToID:   map[string]string{"clay.example.app": "s-clay", "moved.example.site": "s-moved"},
	}
	for _, tc := range []struct{ host, uri, want, path string }{
		{"olive.example.site", "/shop/x.html", "s-shop", "/x.html"},
		{"shop.olive.example.site", "/", "s-shop", "/"},
		{"shop.olive.example.app", "/", "s-shop", "/"},
		{"clay.example.site", "/", "s-clay", "/"},
		{"clay.example.app", "/", "s-clay", "/"},
		{"moved.example.app", "/p", "s-moved", "/p"},
		{"moved.example.site", "/p", "s-moved", "/p"},
		{"legacy.example.site", "/", "s-legacy", "/"},
		{"a.b.c.example.site", "/", "", ""},
		{"shop.olive.example.other", "/", "", ""},
	} {
		got, path := i.attribute(tc.host, tc.uri, m)
		if got != tc.want || (got != "" && path != tc.path) {
			t.Errorf("%s%s: got %q %q want %q %q", tc.host, tc.uri, got, path, tc.want, tc.path)
		}
	}
	// Without the base, today's attribution exactly.
	plain := NewIngester(nil, "", "s", "sites.example.app", "example.app").WithBases("example.app")
	if got, _ := plain.attribute("olive.example.site", "/shop/", m); got != "" {
		t.Fatalf("base hit attributed without the base: %q", got)
	}
}

// A hit on an address family's <label>.<suffix> counts for the family
// owner's site <prefix><label>; an exact custom domain under it wins.
func TestAttributeFamily(t *testing.T) {
	i := NewIngester(nil, "", "s", "sites.example.app", "example.app")
	m := &attrMaps{
		handleToUser: map[string]string{},
		userNameToID: map[string]string{"u1/voucher-meera": "s1", "u1/meera": "s2", "u2/zed": "s9"},
		nameToOldest: map[string]string{},
		domainToID:   map[string]string{"special.voucher.example.com": "s3"},
		families:     map[string]familyAttr{"voucher.example.com": {userID: "u1", prefix: "voucher-"}, "quotes.example.com": {userID: "u1"}},
	}
	for host, want := range map[string]string{
		"meera.voucher.example.com":   "s1",
		"meera.quotes.example.com":    "s2",
		"special.voucher.example.com": "s3",
		"zed.quotes.example.com":      "",
		"a.meera.voucher.example.com": "",
		"voucher.example.com":         "",
	} {
		if got, _ := i.attribute(host, "/x", m); got != want {
			t.Errorf("%s: %q, want %q", host, got, want)
		}
	}
}

// The ninth field is the bytes sent; older lines carry none and count 0.
func TestParseTSVBytes(t *testing.T) {
	base := "2026-09-30T10:00:00+00:00\tshop.example\t200\tGET\t/\t203.0.113.1\tMozilla/5.0"
	for line, want := range map[string]int64{
		base:                   0,
		base + "\t":            0,
		base + "\t\t5120":      5120,
		base + "\tx.com\t5120": 5120,
		base + "\t\t-":         0,
		base + "\t\t-5":        0,
	} {
		l, ok := parseTSV(line)
		if !ok || l.bytes != want {
			t.Errorf("%q: bytes %d ok %v, want %d", line, l.bytes, ok, want)
		}
	}
	l, ok := parseCaddyJSON(`{"msg":"handled request","ts":1759226400.5,"status":200,"size":4096,"request":{"method":"GET","host":"a.example","uri":"/"}}`)
	if !ok || l.bytes != 4096 {
		t.Errorf("caddy size: %d %v", l.bytes, ok)
	}
}
