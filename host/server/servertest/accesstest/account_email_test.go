package accesstest

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
)

var undoLinkRe = regexp.MustCompile(`/v1/me/email/undo#t=([0-9a-f]{48})`)

func TestEmailChangeMovesAccountAndTellsOldAddress(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "mover")
	newAddr := Uniq("moved") + "@example.org"
	host := "simple-host.test"
	key := p.Header()
	uid := p.ID
	ctx := context.Background()

	// A sign-in code still outstanding for the old address must not later
	// create a fresh account under it.
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": p.Email}, nil); r.Status != 202 {
		t.Fatalf("sign-in request: %d %s", r.Status, r.Body)
	}
	staleCode := mb.Code(p.Email)
	// Another key of the account.
	other, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(ctx, a.Database, uid, other, "agent sign-in"); err != nil {
		t.Fatal(err)
	}

	code, cur := RequestEmailChange(t, a, mb, key, p.Email, strings.ToUpper(newAddr))
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
	// A new request starts over.
	code, cur = RequestEmailChange(t, a, mb, key, p.Email, newAddr)
	r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	var username string
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE username = $1`, newAddr).Scan(&username); err != nil {
		t.Fatalf("account not moved: %v", err)
	}
	// The old address is told, with an undo link, and never sees the new address in full.
	var notice string
	for _, n := range mb.To(p.Email) {
		if strings.Contains(n, "was changed to") {
			notice = n
		}
	}
	if !undoLinkRe.MatchString(notice) || strings.Contains(notice, newAddr) {
		t.Fatalf("old-address notice: %q", notice)
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

	// "This wasn't me": a GET or a peek only shows the confirmation; pressing
	// it puts the account back, signs out every key and drops sign-ins linked
	// since the change.
	if _, err := a.Database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, uid, Uniq("g"), newAddr); err != nil {
		t.Fatal(err)
	}
	tok := undoLinkRe.FindStringSubmatch(notice)[1]
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if r := a.At(t, "GET", host, "/v1/me/email/undo?t="+tok, nil, nil); r.Status != 200 {
		t.Fatalf("undo page: %d", r.Status)
	}
	if r := a.At(t, "POST", host, "/v1/me/email/undo", "peek=1&t="+tok, form); r.Status != 200 {
		t.Fatalf("peek: %d", r.Status)
	}
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, uid).Scan(&username); err != nil || username != newAddr {
		t.Fatalf("a GET or peek undid the change: %q %v", username, err)
	}
	if r := a.At(t, "POST", host, "/v1/me/email/undo", "t="+tok, form); r.Status != 200 {
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
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "stay")
	other := a.NewPerson(t, "taken")
	host := "simple-host.test"
	key := p.Header()
	ask := func(addr string, hdr map[string]string) Resp {
		return a.At(t, "POST", host, "/v1/me/email", map[string]string{"email": addr}, hdr)
	}

	// Not the person's own key.
	if r := ask("x@example.org", map[string]string{"X-API-Key": a.Admin}); r.Status != 400 || r.Code() != "not_an_account_key" {
		t.Fatalf("admin key: %d %s", r.Status, r.Body)
	}
	if r := ask(p.Email, key); r.Status != 400 || r.Code() != "same_email" {
		t.Fatalf("same email: %d %s", r.Status, r.Body)
	}
	if r := ask("nope", key); r.Status != 400 {
		t.Fatalf("bad email: %d %s", r.Status, r.Body)
	}
	// Another account's address: the code goes out like any other (asking
	// reveals nothing); only verified codes learn it is taken.
	code, cur := RequestEmailChange(t, a, mb, key, p.Email, other.Email)
	r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key)
	if r.Status != 409 || r.Code() != "email_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	var n int
	_ = a.Database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, p.Email).Scan(&n)
	if n != 1 {
		t.Fatal("account moved despite the address being taken")
	}

	// Nobody moves onto the reviewer account's address; the reviewer account's email is the operator's.
	rv := Uniq("reviewer") + "@example.org"
	a.Users.SetReviewerEmail(rv)
	if r := ask(strings.ToUpper(rv), key); r.Status != 400 || r.Code() != "reserved_email" {
		t.Fatalf("to the reviewer address: %d %s", r.Status, r.Body)
	}
	a.Users.SetReviewerEmail(p.Email)
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "reviewer_account" {
		t.Fatalf("reviewer: %d %s", r.Status, r.Body)
	}
	a.Users.SetReviewerEmail("")

	// Preview, event and admin accounts, and a name that is not an address.
	a.Users.SetPreviewAccounts(map[string]bool{strings.ToLower(p.Email): true})
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "preview_account" {
		t.Fatalf("preview account: %d %s", r.Status, r.Body)
	}
	a.Users.SetPreviewAccounts(nil)
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, p.ID, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, p.ID); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", key); r.Status != 403 || r.Code() != "event_account" {
		t.Fatalf("event account: %d %s", r.Status, r.Body)
	}
	tk, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, Uniq("team"), tk, false); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", map[string]string{"X-API-Key": tk}); r.Status != 403 || r.Code() != "no_current_email" {
		t.Fatalf("no address to confirm from: %d %s", r.Status, r.Body)
	}
	ak, _ := auth.GenerateAPIKey()
	if _, err := accountsrepo.CreateUser(context.Background(), a.Database, Uniq("adm")+"@example.org", ak, true); err != nil {
		t.Fatal(err)
	}
	if r := ask("y@example.org", map[string]string{"X-API-Key": ak}); r.Status != 403 || r.Code() != "admin_account" {
		t.Fatalf("admin account: %d %s", r.Status, r.Body)
	}
}

// The owner app lists linked Google/GitHub sign-ins and can unlink one;
// own key only, own sign-ins only.
func TestIdentitiesListAndUnlink(t *testing.T) {
	a, _ := NewAccountApp(t)
	p, q := a.NewPerson(t, "linked"), a.NewPerson(t, "stranger")
	host := "simple-host.test"
	var mine, theirs string
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'p@oldco.example', true) RETURNING id`, p.ID, Uniq("g")).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, 'q@example', true) RETURNING id`, q.ID, Uniq("g")).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	key := p.Header()
	r := a.At(t, "GET", host, "/v1/me/identities", nil, key)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"id":"`+mine+`"`) || strings.Contains(string(r.Body), theirs) {
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
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "alerted")
	host := "simple-host.test"

	SignInWith(t, a, mb, p.Email, UaMacChrome)
	if got := mb.To(p.Email); len(got) != 1 || strings.Contains(strings.ReplaceAll(got[0], a.Srv.URL, ""), "127.0.0.1") {
		t.Fatalf("first alert (one, with no IP): %v", got)
	}
	// Same browser, same day: no second email. Another browser: one more.
	SignInWith(t, a, mb, p.Email, UaMacChrome)
	if n := len(mb.To(p.Email)); n != 1 {
		t.Fatalf("same browser alerted again: %d", n)
	}
	SignInWith(t, a, mb, p.Email, UaIPhone)
	if n := len(mb.To(p.Email)); n != 2 {
		t.Fatalf("second browser: %d", n)
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
	r := a.At(t, "PATCH", host, "/v1/me", map[string]any{"signin_alerts": false}, p.Header())
	if r.Status != 200 || r.JSON(t)["signin_alerts"] != false {
		t.Fatalf("turn off: %d %s", r.Status, r.Body)
	}
	SignInWith(t, a, mb, p.Email, "curl/8.5.0")
	if n := len(mb.To(p.Email)); n != 2 {
		t.Fatalf("alert sent while off: %d", n)
	}
}

func TestSignInAlertsSkipEventAndReviewer(t *testing.T) {
	a, mb := NewAccountApp(t)
	ev := a.NewPerson(t, "event")
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, ev.ID, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, ev.ID); err != nil {
		t.Fatal(err)
	}
	SignInWith(t, a, mb, ev.Email, UaMacChrome)
	if n := len(mb.To(ev.Email)); n != 0 {
		t.Fatalf("event account alerted: %d", n)
	}

	rv := a.NewPerson(t, "reviewer")
	a.Users.SetReviewerEmail(rv.Email)
	defer a.Users.SetReviewerEmail("")
	SignInWith(t, a, mb, rv.Email, UaMacChrome)
	if n := len(mb.To(rv.Email)); n != 0 {
		t.Fatalf("reviewer alerted: %d", n)
	}
}

func TestConnectAppAlerts(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "connects")
	redirect := "https://chat.example.com/cb"
	client := a.RegisterClient(t, redirect)
	a.Connect(t, p, client, redirect)
	if got := mb.To(p.Email); len(got) != 1 {
		t.Fatalf("connect alert: %v", got)
	}
}
