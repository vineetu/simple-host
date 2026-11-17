package redirects

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	tests := []struct {
		rule, path, query, prefix, want string
		status                          int
		force                           bool
	}{
		{"/hello%20world /new", "/hello world", "", "", "/new", 301, false},
		{"/old /new", "/old/", "tag=a", "", "/new?tag=a", 301, false},
		{"/old/ /new 302!", "/old", "", "/ann/blog", "/ann/blog/new", 302, true},
		{"/cart/* /shop/:splat", "/cart/a/b", "", "", "/shop/a/b", 301, false},
		{"/cart/* /shop/:splat", "/cart", "", "", "/shop/", 301, false},
		{"/cart/* /shop/:splat", "/cart/", "", "", "/shop/", 301, false},
		{"/post/:id /article/:id?q=:id", "/post/a b", "ignored=yes", "", "/article/a%20b?q=a+b", 301, false},
		{"/page id=:id /new/:id 301", "/page", "id=123&extra=x", "", "/new/123", 301, false},
		{"/page lang=en /english", "/page", "lang=en&extra=x", "", "/english", 301, false},
		{"/old /gone.html 410", "/old", "", "", "/gone.html", 410, false},
		{"/old /404.html 404!", "/old", "", "", "/404.html", 404, true},
		{"/old /new?lang=en", "/old", "extra=x", "", "/new?lang=en", 301, false},
		{"/old https://example.com/new/:id 302", "/old", "", "", "", 0, false},
		{"/ /home", "/", "", "", "/home", 301, false},
		{"/x/:id https://example.com/new/:id", "/x/a b", "", "", "https://example.com/new/a%20b", 301, false},
	}
	for _, tt := range tests {
		t.Run(tt.rule+tt.path, func(t *testing.T) {
			rs, err := Parse([]byte(tt.rule), 1000)
			if tt.status == 0 {
				if err == nil {
					t.Fatal("expected undefined placeholder")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			q, _ := url.ParseQuery(tt.query)
			m, ok := rs.Match(tt.path, q)
			if !ok {
				t.Fatal("no match")
			}
			if got := m.Location(tt.prefix, tt.query); got != tt.want || m.Status != tt.status || m.Force != tt.force {
				t.Fatalf("got %q %d %v", got, m.Status, m.Force)
			}
		})
	}
}
func TestNoMatch(t *testing.T) {
	for _, tt := range []struct{ rule, p, q string }{{"/ /home", "/other", ""}, {"/ /home", "//", ""}, {"/a/:id /b/:id", "/a/", ""}, {"/page id=:id /new/:id", "/page", ""}, {"/page lang=en /new", "/page", "lang=fr"}, {"/a /b", "/A", ""}} {
		rs, e := Parse([]byte(tt.rule), 1000)
		if e != nil {
			t.Fatal(e)
		}
		q, _ := url.ParseQuery(tt.q)
		if _, ok := rs.Match(tt.p, q); ok {
			t.Errorf("unexpected match: %+v", tt)
		}
	}
}
func TestInvalid(t *testing.T) {
	for _, line := range []string{"broken", "https://example.com/a /b", "/a?b=c /b", "/../a /b", "/a\\b /b", "/a/*/b /c", "/a/:1 /b", "/a /b 200", "/a /b 308", "/a /b 999!", "/a //evil.com", "/a /\\evil", "/a javascript:alert(1)", "/a /b/:missing", "/a /:splat", "/a https://:host/path", "/a https://example.com/path 410", "/a /a", "/a/ /a", "/a/* /a/:splat", "/blog/* /blog/new/:splat", "/a /b\n/b /a", "/a bad /b", string([]byte{0xff}), "/a /b\x00", strings.Repeat("x", 2049)} {
		t.Run(line[:min(len(line), 60)], func(t *testing.T) {
			_, err := Parse([]byte("# comment\n"+line), 1000)
			if err == nil || !strings.Contains(err.Error(), "line 2:") {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, e := Parse(make([]byte, MaxFileBytes+1), 1000); e == nil {
		t.Fatal("size accepted")
	}
	if _, e := Parse([]byte("/a /b\n/c /d"), 1); e == nil || !strings.Contains(e.Error(), "line 2:") {
		t.Fatal(e)
	}
}
func TestCommentsAndFirstWins(t *testing.T) {
	rs, e := Parse([]byte("\n# comment\n/a /first # explanation\n/a /second\n"), 1000)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := rs.Match("/a", nil)
	if m.Location("", "") != "/first" {
		t.Fatal(m)
	}
}
func TestCapturedInjection(t *testing.T) {
	rs, e := Parse([]byte("/take/* /:splat 302\n/id/:id /new/:id?q=:id"), 1000)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := url.PathUnescape("/id/%0d%0a")
	for _, p := range []string{encoded, "/take///evil.com", "/take/\\evil", "/take/\r\nLocation: evil", "/id/\r\n"} {
		m, ok := rs.Match(p, nil)
		if !ok {
			t.Fatal(p)
		}
		s := m.Location("", "")
		if unsafe(s) || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
			t.Fatalf("unsafe %q", s)
		}
	}
}
func TestRealisticFixture(t *testing.T) {
	var data strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&data, "/blog/2020/post-%d /articles/post-%d 301\n", i, i)
	}
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&data, "/ p=%d /articles/post-%d 301!\n", i, i)
	}
	data.WriteString("/tours/* /travel/:splat\n/cart/:id /shop/:id 302\n")
	rs, e := Parse([]byte(data.String()), 1000)
	if e != nil {
		t.Fatal(e)
	}
	if len(rs.rules) != 70 {
		t.Fatal(len(rs.rules))
	}
	for _, tt := range []struct{ p, q, want string }{{"/blog/2020/post-23", "", "/articles/post-23"}, {"/", "p=4", "/articles/post-4"}, {"/tours/india/south", "", "/travel/india/south"}, {"/cart/42", "", "/shop/42"}} {
		q, _ := url.ParseQuery(tt.q)
		m, ok := rs.Match(tt.p, q)
		if !ok || m.Location("", tt.q) != tt.want {
			t.Fatalf("failed %+v", tt)
		}
	}
}

// A target that is a page of the published site ends the chain for a
// non-forced rule (the page shadows the rule), so it is not a loop; a forced
// rule still is. Serving skips the loop check (validated on publish).
func TestLoopCheckKnowsPublishedPages(t *testing.T) {
	exists := func(p string) bool { return strings.HasPrefix(p, "/tour/main") }
	for _, tc := range []struct {
		file string
		ok   bool
	}{
		{"/tour/* /tour/main/ 301", true},
		{"/tour/* /tour/main/ 301!", false},
		{"/docs/* /docs/v2/:splat", false},
		{"/a /tour/main\n/tour/main /a", true},
	} {
		_, err := ParseWith([]byte(tc.file), Options{MaxRules: 1000, Exists: exists})
		if (err == nil) != tc.ok {
			t.Errorf("%q: err=%v, want ok=%v", tc.file, err, tc.ok)
		}
		if _, err := ParseWith([]byte(tc.file), Options{MaxRules: 1000, SkipLoopCheck: true}); err != nil {
			t.Errorf("%q with SkipLoopCheck: %v", tc.file, err)
		}
	}
}

// Ordered rules are followed the way a browser would: an earlier rule that
// ends the chain makes a later overlapping one fine, while long cycles and
// growing chains are refused.
func TestLoopCheckFollowsRuleOrder(t *testing.T) {
	var ring strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&ring, "/p%d /p%d\n", i, (i+1)%10)
	}
	for _, tc := range []struct {
		file string
		ok   bool
	}{
		{"/docs/v2/* /archive/:splat\n/docs/* /docs/v2/:splat", true},
		{"/:part/* /:splat", true},
		{ring.String(), false},
		{"/a/* /b/:splat\n/b/* /a/x/:splat", false},
		{"/a /b\n/b /c\n/c /d", true},
	} {
		if _, err := Parse([]byte(tc.file), 1000); (err == nil) != tc.ok {
			t.Errorf("%q: err=%v, want ok=%v", tc.file, err, tc.ok)
		}
	}
}

// Round-2 review cases: exactly MaxChain redirects end fine; a page named
// like the sample value cannot hide a loop; dot segments stay inside the
// site's mount.
func TestChainEdgesAndDotSegments(t *testing.T) {
	var chain strings.Builder
	for i := 0; i < MaxChain; i++ {
		fmt.Fprintf(&chain, "/p%d /p%d\n", i, i+1)
	}
	if _, err := ParseWith([]byte(chain.String()), Options{MaxRules: 1000, Exists: func(p string) bool { return p == "/p10" }}); err != nil {
		t.Fatalf("10 redirects rejected: %v", err)
	}
	fmt.Fprintf(&chain, "/p%d /p%d\n", MaxChain, MaxChain+1)
	if _, err := Parse([]byte(chain.String()), 1000); err == nil {
		t.Fatal("11 redirects accepted")
	}
	shadow := func(p string) bool { return p == "/a/sample" || p == "/b/sample" }
	if _, err := ParseWith([]byte("/a/:id /b/:id\n/b/:id /a/:id"), Options{MaxRules: 1000, Exists: shadow}); err == nil {
		t.Fatal("loop hidden by a page named sample")
	}
	if _, err := Parse([]byte("/x /../other"), 1000); err == nil {
		t.Fatal("literal .. target accepted")
	}
	rs, err := Parse([]byte("/go next=:next /:next 302"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"..", ".", "../..", "%2e%2e"} {
		q := url.Values{"next": {v}}
		m, ok := rs.Match("/go", q)
		if !ok {
			t.Fatal("no match")
		}
		if got := m.Location("/ann/blog", ""); !strings.HasPrefix(got, "/ann/blog/") {
			t.Errorf("next=%q: Location %q leaves the mount", v, got)
		}
	}
}
