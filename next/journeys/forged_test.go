package journeys_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	access "simplehost/access/core"
	"simplehost/core/testkit"
)

// Review A1: a page on a sibling address cannot act with the platform's
// cookie or a site's visitor cookie, and /mcp never reads a cookie.
func TestForgedRequests(t *testing.T) {
	k := testkit.Start(t, all...)
	v := k.Person(t, "victim")
	k.Publish(t, v, "blog", map[string]string{"index.html": "original"})
	b := k.Browser()
	secret := access.NewSecret("")
	k.Exec(t, `INSERT INTO sessions (id_hash, person_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, access.Hash(secret), v.ID)
	u, _ := url.Parse(platform)
	b.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-sh_session", Value: secret, Path: "/", Secure: true}})

	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("index.html")
	f.Write([]byte("defaced"))
	zw.Close()
	for _, h := range [][]string{
		{"Content-Type", "text/plain", "Origin", "https://evil.simple-host.app", "Sec-Fetch-Site", "same-site"},
		{"Content-Type", "text/plain", "Origin", "https://evil.simple-host.app", "X-SH-CSRF", "1"},
		{"Content-Type", "text/plain", "Origin", "", "Sec-Fetch-Site", "same-site"},
	} {
		if r := testkit.Call(t, b, "PUT", platform+"/v1/sites/blog", zb.Bytes(), h...); r.Status != 403 || !strings.Contains(r.Body, "csrf_header") {
			t.Fatalf("forged publish %v: %d %s", h, r.Status, r.Body)
		}
	}
	if r := testkit.Call(t, k.Browser(), "GET", "https://blog--victim.simple-host.site/", nil); r.Body != "original" {
		t.Fatalf("site after forged publish: %q", r.Body)
	}
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"publish_site","arguments":{"site":"blog","files":{"index.html":"PWNED"}}}}`
	testkit.Call(t, b, "POST", platform+"/mcp", call, "Content-Type", "application/json", "X-SH-CSRF", "1")
	if r := testkit.Call(t, k.Browser(), "GET", "https://blog--victim.simple-host.site/", nil); r.Body != "original" {
		t.Fatalf("/mcp acted with the cookie: %q", r.Body)
	}
	// The dashboard's own calls still work.
	if r := testkit.Call(t, b, "POST", platform+"/v1/keys", map[string]string{"name": "x"}, "X-SH-CSRF", "1"); r.Status != 201 {
		t.Fatalf("own page: %d %s", r.Status, r.Body)
	}

	// A visitor signed in on blog: a page on another site cannot write as them.
	k.Exec(t, `UPDATE sites SET access = 'viewers' WHERE name = 'blog'`)
	k.SignIn(t, b, v, "https://blog--victim.simple-host.site/")
	if r := testkit.Call(t, b, "POST", "https://blog--victim.simple-host.site/_sh/signout", map[string]any{},
		"Origin", "https://other--mallory.simple-host.site", "X-SH-CSRF", "1"); r.Status != 403 {
		t.Fatalf("site B acting on site A: %d %s", r.Status, r.Body)
	}
}
