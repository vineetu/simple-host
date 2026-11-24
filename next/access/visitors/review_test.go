package visitors_test

import (
	"bytes"
	"net/url"
	"regexp"
	"testing"

	access "simplehost/access/core"
	"simplehost/access/signin"
	"simplehost/access/visitors"
	"simplehost/core/testkit"
)

// Review A11: a visitor session is stored as the hash of its cookie, never
// the cookie itself, and the cookie still signs in.
func TestVisitorSessionIsStoredHashed(t *testing.T) {
	k := testkit.Start(t, signin.Mount, visitors.Mount)
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "a", map[string]string{"index.html": "a"})
	v := k.Browser()
	h := hop(t, v, siteA)
	testkit.Call(t, v, "POST", "https://simple-host.app/v1/auth/code", map[string]string{"email": "vera@example.com"})
	code := regexp.MustCompile(`\d{6}`).FindString(k.Mail.To("vera@example.com")[0].Text)
	next, _ := verify(t, v, map[string]any{"email": "vera@example.com", "code": code, "hop": h})
	finish(t, v, next)
	u, _ := url.Parse(siteA)
	var cookie string
	for _, c := range v.Jar.Cookies(u) {
		if c.Name == "__Host-sh_vsess" {
			cookie = c.Value
		}
	}
	var stored []byte
	k.App.DB.QueryRow(`SELECT id FROM visitor_sessions`).Scan(&stored)
	if cookie == "" || !bytes.Equal(stored, access.SiteSessionID(cookie)) {
		t.Fatalf("stored %x for cookie %q", stored, cookie)
	}
	if in, _ := me(t, v, siteA); !in {
		t.Fatal("the cookie no longer signs in")
	}
}
