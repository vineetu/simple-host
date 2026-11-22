package accesstest

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	. "github.com/vsriram/simple-host/host/access/featuretest"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

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

	code, cur := AskEmailChange(t, a.Base, mb, key, p.Email, strings.ToUpper(newAddr))
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
	code, cur = AskEmailChange(t, a.Base, mb, key, p.Email, newAddr)
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

// TestSelfHostedEmailsNeverNameHostedService covers proof (f): with neither
// SUPPORT_EMAIL nor IDLE_REPLY_TO set, a self-hosted install must never
// email anyone a message naming simple-hack.app, simple-host.app or
// support@simple-host.app. There is no local mail transport to run the
// real Resend sender against (host/email only has Resend), so this
// drives the same fake capturing mailer the handler package's other tests
// already use (account_email_test.go's mailbox) through every email kind
// reachable from here: an email sign-in code, a sign-in alert, and both
// email-change codes/notices.
func TestSelfHostedEmailsNeverNameHostedService(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	auth.SupportContact = "whoever runs this server" // neither setting configured
	web.SetInstanceHosts("hack.example.test", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")

	a, mb := NewAccountApp(t)

	p := a.NewPerson(t, "selfhosted-mail")
	key := p.Header()
	handle := p.Handle

	// ---- 1. email sign-in code + the sign-in alert it triggers on
	//         completion (finishSignIn -> SignInAlerts.Alert) ----
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": p.Email}, nil); r.Status != http.StatusAccepted {
		t.Fatalf("sign-in request: %d %s", r.Status, r.Body)
	}
	mb.Mu.Lock()
	code := mb.Codes[p.Email]
	mb.Mu.Unlock()
	if code == "" {
		t.Fatal("no sign-in code captured")
	}
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": p.Email, "code": code}, nil); r.Status != http.StatusOK {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}

	// ---- 2. email change: both the new-address code and the
	//         current-address notice (host/app/accounts/email_change_mail.go) ----
	newAddr := "selfhosted-mail-moved-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org"
	newCode, curCode := AskEmailChange(t, a.Base, mb, key, p.Email, newAddr)
	if r := a.At(t, "POST", a.AppHost(), "/v1/me/email/verify", map[string]string{"code": newCode, "current_code": curCode}, key); r.Status != http.StatusOK {
		t.Fatalf("email change verify: %d %s", r.Status, r.Body)
	}

	// ---- 3. the account-deletion notice (account_data.go's SupportText
	//         path, the other SendNotice call site besides email-change) ----
	if r := a.At(t, "DELETE", a.AppHost(), "/v1/me", map[string]string{"confirm": handle}, key); r.Status != http.StatusNoContent {
		t.Fatalf("delete account: %d %s", r.Status, r.Body)
	}

	if hits := hostedDomainLeak(mb, nil); len(hits) > 0 {
		t.Errorf("self-hosted install named the hosted service in %d email(s):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}

func TestSignInAlerts(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "alerted")
	host := "simple-host.test"

	SignInWith(t, a.Base, mb, p.Email, UaMacChrome)
	if got := mb.To(p.Email); len(got) != 1 || strings.Contains(strings.ReplaceAll(got[0], a.Srv.URL, ""), "127.0.0.1") {
		t.Fatalf("first alert (one, with no IP): %v", got)
	}
	// Same browser, same day: no second email. Another browser: one more.
	SignInWith(t, a.Base, mb, p.Email, UaMacChrome)
	if n := len(mb.To(p.Email)); n != 1 {
		t.Fatalf("same browser alerted again: %d", n)
	}
	SignInWith(t, a.Base, mb, p.Email, UaIPhone)
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
	SignInWith(t, a.Base, mb, p.Email, "curl/8.5.0")
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
	SignInWith(t, a.Base, mb, ev.Email, UaMacChrome)
	if n := len(mb.To(ev.Email)); n != 0 {
		t.Fatalf("event account alerted: %d", n)
	}

	rv := a.NewPerson(t, "reviewer")
	a.Users.SetReviewerEmail(rv.Email)
	defer a.Users.SetReviewerEmail("")
	SignInWith(t, a.Base, mb, rv.Email, UaMacChrome)
	if n := len(mb.To(rv.Email)); n != 0 {
		t.Fatalf("reviewer alerted: %d", n)
	}
}

var undoLinkRe = regexp.MustCompile(`/v1/me/email/undo#t=([0-9a-f]{48})`)

// hostedDomainLeak reports every captured string (sign-in codes, the
// sign-in link, and every notice's to|subject|text) that names either
// hosted domain or the hosted support address, case-insensitively.
func hostedDomainLeak(mb *Mailbox, links []string) []string {
	var hits []string
	check := func(label, s string) {
		low := strings.ToLower(s)
		if strings.Contains(low, "simple-hack.app") || strings.Contains(low, "simple-host.app") {
			hits = append(hits, label+": "+s)
		}
	}
	mb.Mu.Lock()
	for addr, code := range mb.Codes {
		check("sign-in code to "+addr, code)
	}
	for _, n := range mb.Notices {
		check("notice", n)
	}
	mb.Mu.Unlock()
	for i, l := range links {
		check("link "+strconv.Itoa(i), l)
	}
	return hits
}
