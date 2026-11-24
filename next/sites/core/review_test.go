package sites_test

import (
	"context"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/serve"
)

// Review A9: what a visit decision reads is never served from the lookup
// cache: a take-down shows at once, even when nobody called Forget.
func TestLookupReadsTheDecisionFresh(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "blog", map[string]string{"index.html": "one"})
	host := "blog--nina.simple-host.site"
	if p, err := k.App.Sites.Lookup(context.Background(), host); err != nil || p.Site.TakenDown {
		t.Fatalf("before: %v %v", p.Site, err)
	}
	k.App.DB.Exec(`UPDATE sites SET taken_down_at = now(), access = 'passcode' WHERE name = 'blog'`) // no Forget
	if p, err := k.App.Sites.Lookup(context.Background(), host); err != nil || !p.Site.TakenDown || p.Site.Access != "passcode" {
		t.Fatalf("after a take-down the cache still says %+v (%v)", p.Site, err)
	}
}

// Coordinator B: a site's main address sends page visits to its own domain,
// else to its short name; data calls on the main address stay.
func TestOneAddressPerSite(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "blog", map[string]string{"index.html": "one"})
	s, _ := k.App.Sites.Get(context.Background(), nina.ID, "blog")
	b := k.Browser()
	main := "https://blog--nina.simple-host.site"
	if r := testkit.Call(t, b, "GET", main+"/about?x=1", nil); r.Status != 404 {
		t.Fatalf("no other address yet: %d %v", r.Status, r.Header.Get("Location"))
	}
	k.Exec(t, `INSERT INTO names (name, kind, site_id) VALUES ('ninablog', 'short', $1)`, s.ID)
	if r := testkit.Call(t, b, "GET", main+"/about?x=1", nil); r.Status != 302 || r.Header.Get("Location") != "https://ninablog.simple-host.app/about?x=1" {
		t.Fatalf("short name: %d %q", r.Status, r.Header.Get("Location"))
	}
	// Round 2: a domain whose ownership passed but whose address or
	// certificate is not ready yet takes nobody away from the working address.
	k.Exec(t, `INSERT INTO hostnames (host, kind, site_id, owner_id, proven_at, status, cert_status) VALUES ('blog.example.com', 'own', $1, $2, now(), 'waiting', 'none')`, s.ID, nina.ID)
	if r := testkit.Call(t, b, "GET", main+"/about", nil); r.Header.Get("Location") != "https://ninablog.simple-host.app/about" {
		t.Fatalf("domain not live yet: %d %q", r.Status, r.Header.Get("Location"))
	}
	k.Exec(t, `UPDATE hostnames SET status = 'live', cert_status = 'issued', cert_expires_at = now() + interval '60 days' WHERE host = 'blog.example.com'`)
	if r := testkit.Call(t, b, "GET", main+"/", nil); r.Status != 302 || r.Header.Get("Location") != "https://blog.example.com/" {
		t.Fatalf("own domain first: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := testkit.Call(t, b, "GET", main+"/v1/sites/blog/storage/kv/x/keys", nil); r.Status == 302 {
		t.Fatal("a data call on the main address was redirected")
	}
	if r := testkit.Call(t, b, "GET", "https://blog.example.com/", nil); r.Status != 200 || r.Body != "one" {
		t.Fatalf("own domain: %d %q", r.Status, r.Body)
	}
}
