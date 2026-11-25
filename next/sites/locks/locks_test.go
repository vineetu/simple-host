package locks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"simplehost/access/signin"
	"simplehost/access/visitors"
	"simplehost/app"
	"simplehost/core/testkit"
	datarules "simplehost/data/rules"
	"simplehost/data/values"
	sites "simplehost/sites/core"
	"simplehost/sites/locks"
	"simplehost/sites/serve"
)

// features mounts the real page serving, values, rules and sign-in beside locks.
func features(a *app.App) {
	for _, m := range []func(*app.App){serve.Mount, values.Mount, datarules.Mount, signin.Mount, visitors.Mount, locks.Mount} {
		m(a)
	}
}

func site(t *testing.T, k *testkit.Kit, who testkit.Who, name string) *sites.Site {
	t.Helper()
	k.Publish(t, who, name, map[string]string{"index.html": "the page"})
	s, err := k.App.Sites.Get(context.Background(), who.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const form = "application/x-www-form-urlencoded"

// SPEC test 26 (F19): the right passcode opens pages and data. Wrong tries
// are slowed, then locked out.
func TestPasscode(t *testing.T) {
	k := testkit.Start(t, features)
	ctx := context.Background()
	ann := k.Person(t, "ann")
	s := site(t, k, ann, "club")
	if err := locks.SetPasscode(ctx, k.App, s, "sesame-42"); err != nil {
		t.Fatal(err)
	}
	owner := func(method, path, body string) {
		if r := testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/club/storage/"+path, body, "X-API-Key", ann.Key); r.Status != 200 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
		}
	}
	owner("PUT", "x/rules", `{"rules": {"_values": {"policies": [{"name": "read", "command": "read", "using": "true"}]}}}`)
	owner("PUT", "kv/x/keys/y", `{"value": "data"}`)
	page, data := "https://club--ann.simple-host.site/", "https://club--ann.simple-host.site/v1/sites/club/storage/kv/x/keys/y"
	b := k.Browser()
	if r := testkit.Call(t, b, "GET", page, nil); r.Status != 401 || !strings.Contains(r.Body, `action="/_sh/passcode"`) {
		t.Fatalf("locked page: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", data, nil); r.Status != 403 || !strings.Contains(r.Body, `"passcode"`) {
		t.Fatalf("locked data: %d %s", r.Status, r.Body)
	}
	start := time.Now()
	if r := testkit.Call(t, b, "POST", page+"_sh/passcode", "passcode=wrong-one&next=/", "Content-Type", form); r.Status != 401 || !strings.Contains(r.Body, "not right") {
		t.Fatalf("wrong passcode: %d %s", r.Status, r.Body)
	}
	if time.Since(start) < 400*time.Millisecond {
		t.Fatal("a wrong passcode was not slowed")
	}
	if r := testkit.Call(t, b, "POST", page+"_sh/passcode", "passcode=sesame-42&next=/", "Content-Type", form); r.Status != 303 || r.Header.Get("Location") != "/" {
		t.Fatalf("right passcode: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", page, nil); r.Status != 200 || r.Body != "the page" {
		t.Fatalf("unlocked page: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", data, nil); r.Status != 200 || !strings.Contains(r.Body, `"data"`) {
		t.Fatalf("unlocked data: %d %s", r.Status, r.Body)
	}
	// The owner sees it again, and signing everyone out ends the unlock.
	s, _ = k.App.Sites.ByID(ctx, s.ID)
	if code, err := locks.Passcode(k.App, s); err != nil || code != "sesame-42" {
		t.Fatalf("passcode again: %q %v", code, err)
	}
	if err := locks.SignEveryoneOut(ctx, k.App, s); err != nil {
		t.Fatal(err)
	}
	if r := testkit.Call(t, b, "GET", page, nil); r.Status != 401 {
		t.Fatalf("after sign everyone out: %d", r.Status)
	}
	// From another address: wrong tries, then even the right one is locked out.
	other := k.Browser()
	for i := 0; i < 5; i++ {
		testkit.Call(t, other, "POST", page+"_sh/passcode", "passcode=guess-000"+string(rune('0'+i)), "Content-Type", form, "X-Real-IP", "198.51.100.9")
	}
	r := testkit.Call(t, other, "POST", page+"_sh/passcode", "passcode=sesame-42", "Content-Type", form, "X-Real-IP", "198.51.100.9")
	if r.Status != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("locked out: %d %s", r.Status, r.Body)
	}
}

// signIn signs email in on the site in browser b with the real visitor
// sign-in: a hop to the platform, an emailed code, the ticket back.
func signIn(t *testing.T, k *testkit.Kit, b *http.Client, email, address string) {
	t.Helper()
	r := testkit.Call(t, b, "GET", address+"_sh/signin?return=/", nil)
	u, err := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || err != nil {
		t.Fatalf("start sign-in: %d %s", r.Status, r.Body)
	}
	testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/code", map[string]string{"email": email})
	mails := k.Mail.To(email)
	code := regexp.MustCompile(`\d{6}`).FindString(mails[len(mails)-1].Text)
	r = testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/verify", map[string]string{"email": email, "code": code, "hop": u.Query().Get("hop")})
	var out struct{ Next string }
	if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 || out.Next == "" {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", out.Next, nil); r.Status != http.StatusFound {
		t.Fatalf("finish sign-in: %d %s", r.Status, r.Body)
	}
}

// SPEC test 28 (F20): a listed email opens the site after sign-in. An
// unlisted one sees "Ask for access", and the owner is told.
func TestNamedViewers(t *testing.T) {
	k := testkit.Start(t, features)
	ctx := context.Background()
	ann := k.Person(t, "ann")
	s := site(t, k, ann, "notes")
	if err := locks.SetViewers(ctx, k.App, s, []string{"bea@example.com"}); err != nil {
		t.Fatal(err)
	}
	page := "https://notes--ann.simple-host.site/"
	if r := testkit.Call(t, k.Browser(), "GET", page, nil); r.Status != 401 || !strings.Contains(r.Body, "/_sh/signin") {
		t.Fatalf("not signed in: %d %s", r.Status, r.Body)
	}
	if v, _ := locks.Viewers(ctx, k.App, s); len(v) != 1 || v[0].SignedIn {
		t.Fatalf("viewers before sign-in: %+v", v)
	}
	bea := k.Browser()
	signIn(t, k, bea, "bea@example.com", page)
	if r := testkit.Call(t, bea, "GET", page, nil); r.Status != 200 || r.Body != "the page" {
		t.Fatalf("listed: %d %s", r.Status, r.Body)
	}
	if v, _ := locks.Viewers(ctx, k.App, s); len(v) != 1 || !v[0].SignedIn {
		t.Fatalf("viewers after sign-in: %+v", v)
	}
	cara := k.Browser()
	signIn(t, k, cara, "cara@example.com", page)
	if r := testkit.Call(t, cara, "GET", page, nil); r.Status != 403 || !strings.Contains(r.Body, "Ask for access") {
		t.Fatalf("unlisted: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, cara, "POST", page+"_sh/ask-access", "next=/", "Content-Type", form); r.Status != 200 || !strings.Contains(r.Body, "told the owner") {
		t.Fatalf("ask: %d %s", r.Status, r.Body)
	}
	var told int
	k.App.DB.QueryRow(`SELECT count(*) FROM events WHERE kind = 'access' AND owner_id = $1 AND site_id = $2 AND body->>'email' = 'cara@example.com'`, ann.ID, s.ID).Scan(&told)
	if told != 1 {
		t.Fatalf("owner told %d times", told)
	}
	// Removing someone ends their access at once.
	if err := locks.SetViewers(ctx, k.App, s, nil); err != nil {
		t.Fatal(err)
	}
	if r := testkit.Call(t, bea, "GET", page, nil); r.Status != 403 {
		t.Fatalf("removed: %d %s", r.Status, r.Body)
	}
}

// SPEC test 29 (F20): with the database down, a named-viewer site shows an
// error, never the page.
func TestNamedViewersDatabaseDown(t *testing.T) {
	k := testkit.Start(t, features)
	ann := k.Person(t, "ann")
	s := site(t, k, ann, "notes")
	if err := locks.SetViewers(context.Background(), k.App, s, []string{"bea@example.com"}); err != nil {
		t.Fatal(err)
	}
	page := "https://notes--ann.simple-host.site/"
	bea := k.Browser()
	signIn(t, k, bea, "bea@example.com", page)
	if r := testkit.Call(t, bea, "GET", page, nil); r.Status != 200 {
		t.Fatalf("before: %d %s", r.Status, r.Body)
	}
	// The list cannot be read: an error, not the page.
	k.Exec(t, `ALTER TABLE viewers RENAME TO viewers_away`)
	if r := testkit.Call(t, bea, "GET", page, nil); r.Status != 500 || strings.Contains(r.Body, "the page") {
		t.Fatalf("list unreadable: %d %s", r.Status, r.Body)
	}
	// The whole database is down.
	k.Exec(t, `ALTER TABLE viewers_away RENAME TO viewers`)
	testkit.Call(t, bea, "GET", page, nil) // the address is remembered in memory
	k.App.DB.Close()
	for _, b := range []*http.Client{bea, k.Browser()} {
		if r := testkit.Call(t, b, "GET", page, nil); r.Status != 500 || strings.Contains(r.Body, "the page") {
			t.Fatalf("database down: %d %s", r.Status, r.Body)
		}
	}
}
