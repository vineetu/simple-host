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

var currentCodeRe = regexp.MustCompile(`together with the one sent to the new address:\s+(\d{6})`)
var undoLinkRe = regexp.MustCompile(`/v1/me/email/undo\?t=([0-9a-f]{48})`)

// requestEmailChange asks to move p to newAddr and returns the code sent to
// the new address and the one sent to the current address.
func requestEmailChange(t *testing.T, a *privateApp, mb *mailbox, key map[string]string, current, newAddr string) (newCode, curCode string) {
	t.Helper()
	r := a.at(t, "POST", "simple-host.test", "/v1/me/email", map[string]string{"email": newAddr}, key)
	if r.status != 202 || r.json(t)["current_email"] != current {
		t.Fatalf("request change: %d %s", r.status, r.body)
	}
	got := mb.to(strings.ToLower(newAddr))
	m := confirmCodeRe.FindStringSubmatch(got[len(got)-1])
	if m == nil {
		t.Fatalf("no code for the new address: %v", got)
	}
	cur := mb.to(current)
	c := currentCodeRe.FindStringSubmatch(cur[len(cur)-1])
	if c == nil {
		t.Fatalf("no code for the current address: %v", cur)
	}
	return m[1], c[1]
}

func TestEmailChangeMovesAccountAndTellsOldAddress(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "mover")
	newAddr := uniq("moved") + "@example.org"
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.key}
	uid, _ := a.userID(t, p)
	ctx := context.Background()

	// A sign-in code still outstanding for the old address must not later
	// create a fresh account under it.
	if r := a.at(t, "POST", host, "/v1/auth", map[string]string{"email": p.email}, nil); r.status != 202 {
		t.Fatalf("sign-in request: %d %s", r.status, r.body)
	}
	mb.mu.Lock()
	staleCode := mb.codes[p.email]
	mb.mu.Unlock()
	// Another key of the account, and an emailed idle-cleanup link.
	other, _ := auth.GenerateAPIKey()
	if err := db.AddAPIKey(ctx, a.database, uid, other, "agent sign-in"); err != nil {
		t.Fatal(err)
	}
	a.deploy(t, p, "quiet")
	if _, err := a.database.Exec(`UPDATE sites SET idle_warned_at = now(), idle_token_hash = '\x01' WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}

	code, cur := requestEmailChange(t, a, mb, key, p.email, strings.ToUpper(newAddr))
	wrong := func(c string) string {
		if c == "000000" {
			return "111111"
		}
		return "000000"
	}
	for _, body := range []map[string]string{
		{"code": code},
		{"code": code, "current_code": wrong(cur)},
		{"code": wrong(code), "current_code": cur},
	} {
		if r := a.at(t, "POST", host, "/v1/me/email/verify", body, key); r.status/100 != 4 {
			t.Fatalf("verify %v: %d %s", body, r.status, r.body)
		}
	}
	// Two wrong guesses so far (the first had no current_code); a new
	// request starts over.
	code, cur = requestEmailChange(t, a, mb, key, p.email, newAddr)
	r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.status != 200 {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	var username string
	if err := a.database.QueryRow(`SELECT username FROM users WHERE username = $1`, newAddr).Scan(&username); err != nil {
		t.Fatalf("account not moved: %v", err)
	}
	var notice string
	for _, n := range mb.to(p.email) {
		if strings.Contains(n, "was changed to") {
			notice = n
		}
	}
	if !strings.Contains(notice, "changed to m***@example.org") || !strings.Contains(notice, "support@simple-host.app") || !undoLinkRe.MatchString(notice) {
		t.Fatalf("old-address notice: %q", notice)
	}
	if strings.Contains(notice, newAddr) {
		t.Fatal("old-address notice shows the new address unmasked")
	}
	// This key still works and /v1/me names the new address; the other key
	// was signed out; the idle link ended.
	me := a.at(t, "GET", host, "/v1/me", nil, key)
	if me.status != 200 || me.json(t)["username"] != newAddr {
		t.Fatalf("me after change: %d %s", me.status, me.body)
	}
	if r := a.at(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": other}); r.status != 401 {
		t.Fatalf("other key after change: %d", r.status)
	}
	var links int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1 AND (idle_token_hash IS NOT NULL OR idle_warned_at IS NOT NULL)`, uid).Scan(&links); err != nil || links != 0 {
		t.Fatalf("idle links after change: %d %v", links, err)
	}
	// The old address's outstanding code is spent: it signs nobody in and
	// creates no account.
	if r := a.at(t, "POST", host, "/v1/auth/verify", map[string]string{"email": p.email, "code": staleCode}, nil); r.status != 401 {
		t.Fatalf("old address's code after the change: %d %s", r.status, r.body)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.email).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account created under the old address: %d %v", n, err)
	}
	// The change is spent too.
	if r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key); r.status != 400 {
		t.Fatalf("second verify: %d %s", r.status, r.body)
	}

	// "This wasn't me": the link shows a button; pressing it puts the account
	// back, signs out every key and drops sign-ins linked since the change.
	if _, err := a.database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, uid, uniq("g"), newAddr); err != nil {
		t.Fatal(err)
	}
	tok := undoLinkRe.FindStringSubmatch(notice)[1]
	page := a.at(t, "GET", host, "/v1/me/email/undo?t="+tok, nil, nil)
	if page.status != 200 || !strings.Contains(string(page.body), `action="/v1/me/email/undo"`) {
		t.Fatalf("undo page: %d %s", page.status, page.body)
	}
	if err := a.database.QueryRow(`SELECT username FROM users WHERE id = $1`, uid).Scan(&username); err != nil || username != newAddr {
		t.Fatalf("a GET undid the change: %q %v", username, err)
	}
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if r := a.at(t, "POST", host, "/v1/me/email/undo", "t="+tok, form); r.status != 200 || !strings.Contains(string(r.body), "again") {
		t.Fatalf("undo: %d %s", r.status, r.body)
	}
	if err := a.database.QueryRow(`SELECT username FROM users WHERE id = $1`, uid).Scan(&username); err != nil || username != p.email {
		t.Fatalf("after undo: %q %v", username, err)
	}
	if r := a.at(t, "GET", host, "/v1/me", nil, key); r.status != 401 {
		t.Fatalf("key after undo: %d", r.status)
	}
	var idents int
	if err := a.database.QueryRow(`SELECT count(*) FROM oauth_identities WHERE user_id = $1`, uid).Scan(&idents); err != nil || idents != 0 {
		t.Fatalf("sign-ins linked since the change: %d %v", idents, err)
	}
	if r := a.at(t, "POST", host, "/v1/me/email/undo", "t="+tok, form); r.status != 404 {
		t.Fatalf("undo reused: %d", r.status)
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
	// reveals nothing); only verified codes learn it is taken.
	code, cur := requestEmailChange(t, a, mb, key, p.email, other.email)
	r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.status != 409 || r.json(t)["code"] != "email_taken" {
		t.Fatalf("taken: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.email).Scan(&n)
	if n != 1 {
		t.Fatal("account moved despite the address being taken")
	}

	// Nobody moves onto the reviewer account's address.
	rv := uniq("reviewer") + "@example.org"
	a.users.SetReviewerEmail(rv)
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": strings.ToUpper(rv)}, key); r.status != 400 || r.json(t)["code"] != "reserved_email" {
		t.Fatalf("to the reviewer address: %d %s", r.status, r.body)
	}
	// The reviewer account's email is the operator's.
	a.users.SetReviewerEmail(p.email)
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.status != 403 || r.json(t)["code"] != "reviewer_account" {
		t.Fatalf("reviewer: %d %s", r.status, r.body)
	}
	a.users.SetReviewerEmail("")

	// Preview, event and admin accounts, and a name that is not an address.
	a.users.SetPreviewAccounts(map[string]bool{strings.ToLower(p.email): true})
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.status != 403 || r.json(t)["code"] != "preview_account" {
		t.Fatalf("preview account: %d %s", r.status, r.body)
	}
	a.users.SetPreviewAccounts(nil)
	uid, _ := a.userID(t, p)
	k, _ := auth.GenerateAPIKey()
	if err := db.AddAPIKey(context.Background(), a.database, uid, k, db.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.status != 403 || r.json(t)["code"] != "event_account" {
		t.Fatalf("event account: %d %s", r.status, r.body)
	}
	team := uniq("team")
	tk, _ := auth.GenerateAPIKey()
	if _, err := db.CreateUser(context.Background(), a.database, team, tk, false); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, map[string]string{"X-API-Key": tk}); r.status != 403 || r.json(t)["code"] != "no_current_email" {
		t.Fatalf("no address to confirm from: %d %s", r.status, r.body)
	}
	adm := uniq("adm") + "@example.org"
	ak, _ := auth.GenerateAPIKey()
	if _, err := db.CreateUser(context.Background(), a.database, adm, ak, true); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, map[string]string{"X-API-Key": ak}); r.status != 403 || r.json(t)["code"] != "admin_account" {
		t.Fatalf("admin account: %d %s", r.status, r.body)
	}
}

// The owner app lists linked Google/GitHub sign-ins and can unlink one;
// own key only, own sign-ins only.
func TestIdentitiesListAndUnlink(t *testing.T) {
	a, _ := newAccountApp(t)
	p, q := a.newPerson(t, "linked"), a.newPerson(t, "stranger")
	pid, _ := a.userID(t, p)
	qid, _ := a.userID(t, q)
	host := "simple-host.test"
	var mine, theirs string
	if err := a.database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'p@oldco.example', true) RETURNING id`, pid, uniq("g")).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'q@example', true) RETURNING id`, qid, uniq("g")).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": p.key}
	r := a.at(t, "GET", host, "/v1/me/identities", nil, key)
	if r.status != 200 || !strings.Contains(string(r.body), `"id":"`+mine+`"`) || !strings.Contains(string(r.body), "p@oldco.example") || strings.Contains(string(r.body), theirs) {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", host, "/v1/me/identities/"+theirs, nil, key); r.status != 404 {
		t.Fatalf("someone else's sign-in: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", host, "/v1/me/identities/"+mine, nil, map[string]string{"X-API-Key": a.admin}); r.status != 400 {
		t.Fatalf("admin key: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", host, "/v1/me/identities/"+mine, nil, key); r.status != 204 {
		t.Fatalf("unlink: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/me/identities", nil, key); strings.Contains(string(r.body), mine) {
		t.Fatalf("still listed: %s", r.body)
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
	if len(got) != 1 || !strings.Contains(got[0], "New sign-in") || !strings.Contains(got[0], `From: "Chrome on macOS" (as reported by the browser or app)`) ||
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
	if len(got) != 1 || !strings.Contains(got[0], `An app ("Test Chat", as reported by the app) was connected`) {
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
	for in, want := range map[string]string{
		"Test Chat":                              "Test Chat",
		"Simple Host Security: call +1-800-555":  "Simple Host Security call +1-800-555",
		"verify at simple-host-support.com now":  "verify at now",
		"write to help@evil.example":             "write to",
		"see https://evil.example/x or www.x.io": "see or",
		"evil-support.com":                       "",
		strings.Repeat("a", 60):                  strings.Repeat("a", 40),
	} {
		if got := reportedName(in); got != want {
			t.Errorf("reportedName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := maskEmail("new.person@example.com"); got != "n***@example.com" {
		t.Errorf("maskEmail = %q", got)
	}
}
