package handler

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// mailbox captures sign-in codes and notices.
type mailbox struct {
	mu      sync.Mutex
	codes   map[string]string // address -> last sign-in code
	notices []string          // "to|subject|text"
}

func (m *mailbox) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *mailbox) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, to+"|"+subject+"|"+text)
	return nil
}

// to returns the notices sent to address.
func (m *mailbox) to(address string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, n := range m.notices {
		if strings.HasPrefix(n, address+"|") {
			out = append(out, n)
		}
	}
	return out
}

func newAccountApp(t *testing.T) (*privateApp, *mailbox) {
	t.Helper()
	mb := &mailbox{}
	a := newPrivateAppMailer(t, mb)
	a.users.alerts.run = func(f func()) { f() }
	// These tests sign the same address in several times.
	a.users.emailLimiter = newRateLimiter(1000, 1000)
	return a, mb
}

var confirmCodeRe = regexp.MustCompile(`confirmation code: (\d{6})`)

func TestEmailChangeMovesAccountAndTellsOldAddress(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "mover")
	newAddr := uniq("moved") + "@example.org"
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.key}

	// A sign-in code still outstanding for the old address must not later
	// create a fresh account under it.
	if r := a.at(t, "POST", host, "/v1/auth", map[string]string{"email": p.email}, nil); r.status != 202 {
		t.Fatalf("sign-in request: %d %s", r.status, r.body)
	}

	r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": strings.ToUpper(newAddr)}, key)
	if r.status != 202 {
		t.Fatalf("request change: %d %s", r.status, r.body)
	}
	got := mb.to(newAddr)
	if len(got) != 1 {
		t.Fatalf("code notices to new address: %v", got)
	}
	m := confirmCodeRe.FindStringSubmatch(got[0])
	if m == nil {
		t.Fatalf("no code in %q", got[0])
	}
	wrong := "000000"
	if m[1] == wrong {
		wrong = "111111"
	}
	if r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": wrong}, key); r.status != 401 {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": m[1]}, key)
	if r.status != 200 {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	var username string
	if err := a.database.QueryRow(`SELECT username FROM users WHERE username = $1`, newAddr).Scan(&username); err != nil {
		t.Fatalf("account not moved: %v", err)
	}
	old := mb.to(p.email)
	if len(old) != 1 || !strings.Contains(old[0], "changed to m***@example.org") || !strings.Contains(old[0], "support@simple-host.app") {
		t.Fatalf("old-address notice: %v", old)
	}
	if strings.Contains(old[0], newAddr) {
		t.Fatal("old-address notice shows the new address unmasked")
	}
	// The key still works and /v1/me names the new address.
	me := a.at(t, "GET", host, "/v1/me", nil, key)
	if me.status != 200 || me.json(t)["username"] != newAddr {
		t.Fatalf("me after change: %d %s", me.status, me.body)
	}
	// The old address's outstanding code is spent.
	var open int
	if err := a.database.QueryRow(`SELECT count(*) FROM auth_tokens WHERE email = $1 AND used_at IS NULL`, p.email).Scan(&open); err != nil || open != 0 {
		t.Fatalf("old address still has %d open codes (%v)", open, err)
	}
	// The change is spent too.
	if r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": m[1]}, key); r.status != 400 {
		t.Fatalf("second verify: %d %s", r.status, r.body)
	}
}

func TestEmailChangeRefusals(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "stay")
	other := a.newPerson(t, "taken")
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.key}

	// Not the person's own key.
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "x@example.org"}, map[string]string{"X-API-Key": a.admin}); r.status != 400 || r.json(t)["code"] != "not_an_account_key" {
		t.Fatalf("admin key: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": p.email}, key); r.status != 400 || r.json(t)["code"] != "same_email" {
		t.Fatalf("same email: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "nope"}, key); r.status != 400 {
		t.Fatalf("bad email: %d %s", r.status, r.body)
	}
	// Another account's address: the code goes out like any other (asking
	// reveals nothing); only a verified code learns it is taken.
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": other.email}, key); r.status != 202 {
		t.Fatalf("request for taken address: %d %s", r.status, r.body)
	}
	code := confirmCodeRe.FindStringSubmatch(mb.to(other.email)[0])[1]
	r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code}, key)
	if r.status != 409 || r.json(t)["code"] != "email_taken" {
		t.Fatalf("taken: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.email).Scan(&n)
	if n != 1 {
		t.Fatal("account moved despite the address being taken")
	}

	// The reviewer account's email is the operator's.
	a.users.SetReviewerEmail(p.email)
	defer a.users.SetReviewerEmail("")
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.status != 403 {
		t.Fatalf("reviewer: %d %s", r.status, r.body)
	}
}

// signIn runs the emailed-code sign-in with the given browser.
func signInWith(t *testing.T, a *privateApp, mb *mailbox, address, ua string) {
	t.Helper()
	host := "simple-host.test"
	if r := a.at(t, "POST", host, "/v1/auth", map[string]string{"email": address}, map[string]string{"User-Agent": ua}); r.status != 202 {
		t.Fatalf("sign-in request: %d %s", r.status, r.body)
	}
	mb.mu.Lock()
	code := mb.codes[address]
	mb.mu.Unlock()
	if r := a.at(t, "POST", host, "/v1/auth/verify", map[string]string{"email": address, "code": code}, map[string]string{"User-Agent": ua}); r.status != 200 {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
}

const (
	uaMacChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	uaIPhone    = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
)

func TestSignInAlerts(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "alerted")
	host := "simple-host.test"

	signInWith(t, a, mb, p.email, uaMacChrome)
	got := mb.to(p.email)
	if len(got) != 1 || !strings.Contains(got[0], "New sign-in") || !strings.Contains(got[0], "From: Chrome on macOS") ||
		!strings.Contains(got[0], " UTC") || !strings.Contains(got[0], a.srv.URL+"/") || !strings.Contains(got[0], "Sign out everywhere") {
		t.Fatalf("first alert: %v", got)
	}
	// The test server itself listens on 127.0.0.1; the sign-in came from there too.
	if strings.Contains(strings.ReplaceAll(got[0], a.srv.URL, ""), "127.0.0.1") {
		t.Fatal("alert carries an IP")
	}
	// Same browser, same day: no second email. Another browser: one more.
	signInWith(t, a, mb, p.email, uaMacChrome)
	if n := len(mb.to(p.email)); n != 1 {
		t.Fatalf("same browser alerted again: %d", n)
	}
	signInWith(t, a, mb, p.email, uaIPhone)
	if got := mb.to(p.email); len(got) != 2 || !strings.Contains(got[1], "Safari on iPhone") {
		t.Fatalf("second browser: %v", got)
	}

	// API key calls never alert.
	a.at(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": p.key, "User-Agent": "Firefox/1"})
	if n := len(mb.to(p.email)); n != 2 {
		t.Fatalf("key call alerted: %d", n)
	}

	// The owner turns alerts off (own key only).
	if r := a.at(t, "PATCH", host, "/v1/me", map[string]any{"signin_alerts": false}, map[string]string{"X-API-Key": a.admin}); r.status != 400 {
		t.Fatalf("admin key toggled alerts: %d %s", r.status, r.body)
	}
	r := a.at(t, "PATCH", host, "/v1/me", map[string]any{"signin_alerts": false}, map[string]string{"X-API-Key": p.key})
	if r.status != 200 || r.json(t)["signin_alerts"] != false {
		t.Fatalf("turn off: %d %s", r.status, r.body)
	}
	if me := a.at(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": p.key}); me.json(t)["signin_alerts"] != false {
		t.Fatalf("me: %s", me.body)
	}
	signInWith(t, a, mb, p.email, "curl/8.5.0")
	if n := len(mb.to(p.email)); n != 2 {
		t.Fatalf("alert sent while off: %d", n)
	}
}

func TestSignInAlertsSkipEventAndReviewer(t *testing.T) {
	a, mb := newAccountApp(t)
	ev := a.newPerson(t, "event")
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, ev.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	k, _ := auth.GenerateAPIKey()
	if err := db.AddAPIKey(context.Background(), a.database, id, k, db.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	signInWith(t, a, mb, ev.email, uaMacChrome)
	if n := len(mb.to(ev.email)); n != 0 {
		t.Fatalf("event account alerted: %d", n)
	}

	rv := a.newPerson(t, "reviewer")
	a.users.SetReviewerEmail(rv.email)
	defer a.users.SetReviewerEmail("")
	signInWith(t, a, mb, rv.email, uaMacChrome)
	if n := len(mb.to(rv.email)); n != 0 {
		t.Fatalf("reviewer alerted: %d", n)
	}
}

func TestConnectAppAlerts(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "connects")
	redirect := "https://chat.example.com/cb"
	client := a.registerClient(t, redirect)
	a.connect(t, p, client, redirect)
	got := mb.to(p.email)
	if len(got) != 1 || !strings.Contains(got[0], `An app called "Test Chat" was connected`) {
		t.Fatalf("connect alert: %v", got)
	}
}

func TestSummarizeUserAgent(t *testing.T) {
	cases := map[string]string{
		uaMacChrome: "Chrome on macOS",
		uaIPhone:    "Safari on iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0": "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0":                                                        "Firefox on Linux",
		"curl/8.5.0":            "curl",
		"python-requests/2.32":  "python-requests",
		"":                      "an unknown browser or app",
		"Mozilla/5.0 (Android)": "a browser on Android",
	}
	for ua, want := range cases {
		if got := summarizeUserAgent(ua); got != want {
			t.Errorf("summarizeUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
	if got := maskEmail("new.person@example.com"); got != "n***@example.com" {
		t.Errorf("maskEmail = %q", got)
	}
}
