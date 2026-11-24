package visitors_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"simplehost/access/signin"
	"simplehost/access/visitors"
	"simplehost/core/testkit"
)

const (
	siteA = "https://a--nina.simple-host.site"
	siteB = "https://b--nina.simple-host.site"
)

// hop starts signing in on a site and returns the hop the platform gets.
func hop(t *testing.T, v *http.Client, site string) string {
	t.Helper()
	r := testkit.Call(t, v, "GET", site+"/_sh/signin?return=/guestbook", nil)
	u, err := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || err != nil || u.Host != "simple-host.app" || u.Query().Get("hop") == "" {
		t.Fatalf("start sign-in on %s: %d %s", site, r.Status, r.Header.Get("Location"))
	}
	return u.Query().Get("hop")
}

func verify(t *testing.T, v *http.Client, in map[string]any) (next, confirm string) {
	t.Helper()
	r := testkit.Call(t, v, "POST", "https://simple-host.app/v1/auth/verify", in)
	var out struct{ Next, Confirm string }
	if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	return out.Next, out.Confirm
}

// finish follows the ticket back to the site.
func finish(t *testing.T, v *http.Client, next string) {
	t.Helper()
	if r := testkit.Call(t, v, "GET", next, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "/guestbook" {
		t.Fatalf("finish %s: %d %s", next, r.Status, r.Body)
	}
}

func me(t *testing.T, v *http.Client, site string, header ...string) (signedIn bool, email string) {
	t.Helper()
	var out struct {
		SignedIn bool `json:"signed_in"`
		Email    string
	}
	json.Unmarshal([]byte(testkit.Call(t, v, "GET", site+"/_sh/me", nil, header...).Body), &out)
	return out.SignedIn, out.Email
}

// SPEC test 25 (F18): a visitor signs in on site A. Site B cannot read it.
// Signed in elsewhere, they only confirm.
func TestVisitorSignInIsPerSite(t *testing.T) {
	k := testkit.Start(t, signin.Mount, visitors.Mount)
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "a", map[string]string{"index.html": "a"})
	k.Publish(t, nina, "b", map[string]string{"index.html": "b"})
	v := k.Browser()

	// On site A, with an emailed code proved on the platform.
	h := hop(t, v, siteA)
	testkit.Call(t, v, "POST", "https://simple-host.app/v1/auth/code", map[string]string{"email": "vera@example.com"})
	code := regexp.MustCompile(`\d{6}`).FindString(k.Mail.To("vera@example.com")[0].Text)
	next, _ := verify(t, v, map[string]any{"email": "vera@example.com", "code": code, "hop": h})
	finish(t, v, next)
	if in, email := me(t, v, siteA); !in || email != "vera@example.com" {
		t.Fatalf("site A: signed in %v as %q", in, email)
	}

	// Site B cannot read it, not even when handed site A's cookie.
	if in, _ := me(t, v, siteB); in {
		t.Fatal("site B reads site A's sign-in")
	}
	u, _ := url.Parse(siteA)
	for _, c := range v.Jar.Cookies(u) {
		if in, _ := me(t, k.Browser(), siteB, "Cookie", c.Name+"="+c.Value); in {
			t.Fatalf("site B accepts site A's cookie %s", c.Name)
		}
	}
	// A ticket for A does not finish on B.
	next, _ = verify(t, v, map[string]any{"hop": hop(t, v, siteA)})
	if r := testkit.Call(t, v, "GET", siteB+"/_sh/signin/return?"+next[len(siteA+"/_sh/signin/return?"):], nil); r.Status != 400 {
		t.Fatalf("A's ticket on B: %d %s", r.Status, r.Body)
	}

	// Signed in on the platform, site B only asks to confirm, once.
	h = hop(t, v, siteB)
	if next, confirm := verify(t, v, map[string]any{"hop": h}); next != "" || confirm != "b--nina.simple-host.site" {
		t.Fatalf("first visit to B: next %q, confirm %q", next, confirm)
	}
	next, _ = verify(t, v, map[string]any{"hop": h, "confirm": true})
	finish(t, v, next)
	if in, _ := me(t, v, siteB); !in {
		t.Fatal("site B after confirming: not signed in")
	}
	if r := testkit.Call(t, v, "POST", siteB+"/_sh/signout", nil); r.Status != 200 {
		t.Fatalf("sign out of B: %d %s", r.Status, r.Body)
	}
	if in, _ := me(t, v, siteB); in {
		t.Fatal("site B after signing out: still signed in")
	}
	if next, _ := verify(t, v, map[string]any{"hop": hop(t, v, siteB)}); next == "" {
		t.Fatal("B asked to confirm a second time")
	}
}
