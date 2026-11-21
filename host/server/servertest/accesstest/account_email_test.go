package accesstest

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
)

var undoLinkRe = regexp.MustCompile(`/v1/me/email/undo#t=([0-9a-f]{48})`)

func TestEmailChangeMovesAccountAndTellsOldAddress(t *testing.T) {
	a, mb := servertest.NewAccountApp(t)
	p := a.NewPerson(t, "mover")
	newAddr := servertest.Uniq("moved") + "@example.org"
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.Key}
	uid, _ := a.UserID(t, p)
	ctx := context.Background()

	// A sign-in code still outstanding for the old address must not later
	// create a fresh account under it.
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": p.Email}, nil); r.Status != 202 {
		t.Fatalf("sign-in request: %d %s", r.Status, r.Body)
	}
	mb.Mu.Lock()
	staleCode := mb.Codes[p.Email]
	mb.Mu.Unlock()
	// Another key of the account.
	other, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(ctx, a.Database, uid, other, "agent sign-in"); err != nil {
		t.Fatal(err)
	}

	code, cur := servertest.RequestEmailChange(t, a, mb, key, p.Email, strings.ToUpper(newAddr))
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
		if r := a.At(t, "POST", host, "/v1/me/email/verify", body, key); r.Status/100 != 4 {
			t.Fatalf("verify %v: %d %s", body, r.Status, r.Body)
		}
	}
	// Two wrong guesses so far (the first had no current_code); a new
	// request starts over.
	code, cur = servertest.RequestEmailChange(t, a, mb, key, p.Email, newAddr)
	r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	var username string
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE username = $1`, newAddr).Scan(&username); err != nil {
		t.Fatalf("account not moved: %v", err)
	}
	var notice string
	for _, n := range mb.To(p.Email) {
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
	// was signed out.
	me := a.At(t, "GET", host, "/v1/me", nil, key)
	if me.Status != 200 || me.JSON(t)["username"] != newAddr {
		t.Fatalf("me after change: %d %s", me.Status, me.Body)
	}
	if r := a.At(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": other}); r.Status != 401 {
		t.Fatalf("other key after change: %d", r.Status)
	}
	// The old address's outstanding code is spent: it signs nobody in and
	// creates no account.
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": p.Email, "code": staleCode}, nil); r.Status != 401 {
		t.Fatalf("old address's code after the change: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.Email).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account created under the old address: %d %v", n, err)
	}
	// The change is spent too.
	if r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key); r.Status != 400 {
		t.Fatalf("second verify: %d %s", r.Status, r.Body)
	}

	// "This wasn't me": the link shows a button; pressing it puts the account
	// back, signs out every key and drops sign-ins linked since the change.
	if _, err := a.Database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, uid, servertest.Uniq("g"), newAddr); err != nil {
		t.Fatal(err)
	}
	tok := undoLinkRe.FindStringSubmatch(notice)[1]
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	// The emailed link keeps the token in the fragment: the GET gets the page
	// that posts it back (peek), which shows the confirmation.
	if r := a.At(t, "GET", host, "/v1/me/email/undo", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), `name="peek" value="1"`) {
		t.Fatalf("fragment page: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", host, "/v1/me/email/undo", "peek=1&t="+tok, form); r.Status != 200 || !strings.Contains(string(r.Body), "Undo the change") {
		t.Fatalf("peek: %d %s", r.Status, r.Body)
	}
	// Links sent before carry ?t= and still open the confirmation.
	page := a.At(t, "GET", host, "/v1/me/email/undo?t="+tok, nil, nil)
	if page.Status != 200 || !strings.Contains(string(page.Body), `action="/v1/me/email/undo"`) {
		t.Fatalf("undo page: %d %s", page.Status, page.Body)
	}
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, uid).Scan(&username); err != nil || username != newAddr {
		t.Fatalf("a GET or peek undid the change: %q %v", username, err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email/undo", "t="+tok, form); r.Status != 200 || !strings.Contains(string(r.Body), "again") {
		t.Fatalf("undo: %d %s", r.Status, r.Body)
	}
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, uid).Scan(&username); err != nil || username != p.Email {
		t.Fatalf("after undo: %q %v", username, err)
	}
	if r := a.At(t, "GET", host, "/v1/me", nil, key); r.Status != 401 {
		t.Fatalf("key after undo: %d", r.Status)
	}
	var idents int
	if err := a.Database.QueryRow(`SELECT count(*) FROM oauth_identities WHERE user_id = $1`, uid).Scan(&idents); err != nil || idents != 0 {
		t.Fatalf("sign-ins linked since the change: %d %v", idents, err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email/undo", "t="+tok, form); r.Status != 404 {
		t.Fatalf("undo reused: %d", r.Status)
	}
}

func TestEmailChangeRefusals(t *testing.T) {
	a, mb := servertest.NewAccountApp(t)
	p := a.NewPerson(t, "stay")
	other := a.NewPerson(t, "taken")
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.Key}

	// Not the person's own key.
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "x@example.org"}, map[string]string{"X-API-Key": a.Admin}); r.Status != 400 || r.JSON(t)["code"] != "not_an_account_key" {
		t.Fatalf("admin key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": p.Email}, key); r.Status != 400 || r.JSON(t)["code"] != "same_email" {
		t.Fatalf("same email: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "nope"}, key); r.Status != 400 {
		t.Fatalf("bad email: %d %s", r.Status, r.Body)
	}
	// Another account's address: the code goes out like any other (asking
	// reveals nothing); only verified codes learn it is taken.
	code, cur := servertest.RequestEmailChange(t, a, mb, key, p.Email, other.Email)
	r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.Status != 409 || r.JSON(t)["code"] != "email_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	var n int
	_ = a.Database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.Email).Scan(&n)
	if n != 1 {
		t.Fatal("account moved despite the address being taken")
	}

	// Nobody moves onto the reviewer account's address.
	rv := servertest.Uniq("reviewer") + "@example.org"
	a.Users.SetReviewerEmail(rv)
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": strings.ToUpper(rv)}, key); r.Status != 400 || r.JSON(t)["code"] != "reserved_email" {
		t.Fatalf("to the reviewer address: %d %s", r.Status, r.Body)
	}
	// The reviewer account's email is the operator's.
	a.Users.SetReviewerEmail(p.Email)
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.Status != 403 || r.JSON(t)["code"] != "reviewer_account" {
		t.Fatalf("reviewer: %d %s", r.Status, r.Body)
	}
	a.Users.SetReviewerEmail("")

	// Preview, event and admin accounts, and a name that is not an address.
	a.Users.SetPreviewAccounts(map[string]bool{strings.ToLower(p.Email): true})
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.Status != 403 || r.JSON(t)["code"] != "preview_account" {
		t.Fatalf("preview account: %d %s", r.Status, r.Body)
	}
	a.Users.SetPreviewAccounts(nil)
	uid, _ := a.UserID(t, p)
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, uid, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, uid); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, key); r.Status != 403 || r.JSON(t)["code"] != "event_account" {
		t.Fatalf("event account: %d %s", r.Status, r.Body)
	}
	team := servertest.Uniq("team")
	tk, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, team, tk, false); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, map[string]string{"X-API-Key": tk}); r.Status != 403 || r.JSON(t)["code"] != "no_current_email" {
		t.Fatalf("no address to confirm from: %d %s", r.Status, r.Body)
	}
	adm := servertest.Uniq("adm") + "@example.org"
	ak, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, adm, ak, true); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": "y@example.org"}, map[string]string{"X-API-Key": ak}); r.Status != 403 || r.JSON(t)["code"] != "admin_account" {
		t.Fatalf("admin account: %d %s", r.Status, r.Body)
	}
}

// The owner app lists linked Google/GitHub sign-ins and can unlink one;
// own key only, own sign-ins only.
func TestIdentitiesListAndUnlink(t *testing.T) {
	a, _ := servertest.NewAccountApp(t)
	p, q := a.NewPerson(t, "linked"), a.NewPerson(t, "stranger")
	pid, _ := a.UserID(t, p)
	qid, _ := a.UserID(t, q)
	host := "simple-host.test"
	var mine, theirs string
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'p@oldco.example', true) RETURNING id`, pid, servertest.Uniq("g")).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'q@example', true) RETURNING id`, qid, servertest.Uniq("g")).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": p.Key}
	r := a.At(t, "GET", host, "/v1/me/identities", nil, key)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"id":"`+mine+`"`) || !strings.Contains(string(r.Body), "p@oldco.example") || strings.Contains(string(r.Body), theirs) {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+theirs, nil, key); r.Status != 404 {
		t.Fatalf("someone else's sign-in: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+mine, nil, map[string]string{"X-API-Key": a.Admin}); r.Status != 400 {
		t.Fatalf("admin key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", host, "/v1/me/identities/"+mine, nil, key); r.Status != 204 {
		t.Fatalf("unlink: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/me/identities", nil, key); strings.Contains(string(r.Body), mine) {
		t.Fatalf("still listed: %s", r.Body)
	}
}

func TestSignInAlerts(t *testing.T) {
	a, mb := servertest.NewAccountApp(t)
	p := a.NewPerson(t, "alerted")
	host := "simple-host.test"

	servertest.SignInWith(t, a, mb, p.Email, servertest.UaMacChrome)
	got := mb.To(p.Email)
	if len(got) != 1 || !strings.Contains(got[0], "New sign-in") || !strings.Contains(got[0], `From: "Chrome on macOS" (as reported by the browser or app)`) ||
		!strings.Contains(got[0], " UTC") || !strings.Contains(got[0], a.Srv.URL+"/") || !strings.Contains(got[0], "Sign out everywhere") {
		t.Fatalf("first alert: %v", got)
	}
	// The test server itself listens on 127.0.0.1; the sign-in came from there too.
	if strings.Contains(strings.ReplaceAll(got[0], a.Srv.URL, ""), "127.0.0.1") {
		t.Fatal("alert carries an IP")
	}
	// Same browser, same day: no second email. Another browser: one more.
	servertest.SignInWith(t, a, mb, p.Email, servertest.UaMacChrome)
	if n := len(mb.To(p.Email)); n != 1 {
		t.Fatalf("same browser alerted again: %d", n)
	}
	servertest.SignInWith(t, a, mb, p.Email, servertest.UaIPhone)
	if got := mb.To(p.Email); len(got) != 2 || !strings.Contains(got[1], "Safari on iPhone") {
		t.Fatalf("second browser: %v", got)
	}

	// API key calls never alert.
	a.At(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": p.Key, "User-Agent": "Firefox/1"})
	if n := len(mb.To(p.Email)); n != 2 {
		t.Fatalf("key call alerted: %d", n)
	}

	// The owner turns alerts off (own key only).
	if r := a.At(t, "PATCH", host, "/v1/me", map[string]any{"signin_alerts": false}, map[string]string{"X-API-Key": a.Admin}); r.Status != 400 {
		t.Fatalf("admin key toggled alerts: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "PATCH", host, "/v1/me", map[string]any{"signin_alerts": false}, map[string]string{"X-API-Key": p.Key})
	if r.Status != 200 || r.JSON(t)["signin_alerts"] != false {
		t.Fatalf("turn off: %d %s", r.Status, r.Body)
	}
	if me := a.At(t, "GET", host, "/v1/me", nil, map[string]string{"X-API-Key": p.Key}); me.JSON(t)["signin_alerts"] != false {
		t.Fatalf("me: %s", me.Body)
	}
	servertest.SignInWith(t, a, mb, p.Email, "curl/8.5.0")
	if n := len(mb.To(p.Email)); n != 2 {
		t.Fatalf("alert sent while off: %d", n)
	}
}

func TestSignInAlertsSkipEventAndReviewer(t *testing.T) {
	a, mb := servertest.NewAccountApp(t)
	ev := a.NewPerson(t, "event")
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = $1`, ev.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, id, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, id); err != nil {
		t.Fatal(err)
	}
	servertest.SignInWith(t, a, mb, ev.Email, servertest.UaMacChrome)
	if n := len(mb.To(ev.Email)); n != 0 {
		t.Fatalf("event account alerted: %d", n)
	}

	rv := a.NewPerson(t, "reviewer")
	a.Users.SetReviewerEmail(rv.Email)
	defer a.Users.SetReviewerEmail("")
	servertest.SignInWith(t, a, mb, rv.Email, servertest.UaMacChrome)
	if n := len(mb.To(rv.Email)); n != 0 {
		t.Fatalf("reviewer alerted: %d", n)
	}
}

func TestConnectAppAlerts(t *testing.T) {
	a, mb := servertest.NewAccountApp(t)
	p := a.NewPerson(t, "connects")
	redirect := "https://chat.example.com/cb"
	client := a.RegisterClient(t, redirect)
	a.Connect(t, p, client, redirect)
	got := mb.To(p.Email)
	if len(got) != 1 || !strings.Contains(got[0], `An app ("Test Chat", as reported by the app) was connected`) {
		t.Fatalf("connect alert: %v", got)
	}
}
