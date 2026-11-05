package handler

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
)

// hostedDomainLeak reports every captured string (sign-in codes, the
// sign-in link, and every notice's to|subject|text) that names either
// hosted domain or the hosted support address, case-insensitively.
func hostedDomainLeak(mb *mailbox, links []string) []string {
	var hits []string
	check := func(label, s string) {
		low := strings.ToLower(s)
		if strings.Contains(low, "simple-hack.app") || strings.Contains(low, "simple-host.app") {
			hits = append(hits, label+": "+s)
		}
	}
	mb.mu.Lock()
	for addr, code := range mb.codes {
		check("sign-in code to "+addr, code)
	}
	for _, n := range mb.notices {
		check("notice", n)
	}
	mb.mu.Unlock()
	for i, l := range links {
		check("link "+strconv.Itoa(i), l)
	}
	return hits
}

// TestSelfHostedEmailsNeverNameHostedService covers proof (f): with neither
// SUPPORT_EMAIL nor IDLE_REPLY_TO set, a self-hosted install must never
// email anyone a message naming simple-hack.app, simple-host.app or
// support@simple-host.app. There is no local mail transport to run the
// real Resend sender against (internal/email only has Resend), so this
// drives the same fake capturing mailer the handler package's other tests
// already use (account_email_test.go's mailbox) through every email kind
// reachable from here: an email sign-in code, a sign-in alert, and both
// email-change codes/notices.
func TestSelfHostedEmailsNeverNameHostedService(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	auth.SupportContact = "whoever runs this server" // neither setting configured
	SetInstanceHosts("hack.example.test", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")

	mb := &mailbox{}
	a := newPrivateAppMailerBase(t, mb, "https://hack.example.test")
	a.users.alerts.run = func(f func()) { f() } // synchronous for the test
	a.users.emailLimiter = newRateLimiter(1000, 1000)

	p := a.newPerson(t, "selfhosted-mail")
	key := map[string]string{"X-API-Key": p.key}
	_, handle := a.userID(t, p)
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, p.email) })

	// ---- 1. email sign-in code + the sign-in alert it triggers on
	//         completion (finishSignIn -> SignInAlerts.Alert) ----
	if r := a.at(t, "POST", a.appHost(), "/v1/auth", map[string]string{"email": p.email}, nil); r.status != http.StatusAccepted {
		t.Fatalf("sign-in request: %d %s", r.status, r.body)
	}
	mb.mu.Lock()
	code := mb.codes[p.email]
	mb.mu.Unlock()
	if code == "" {
		t.Fatal("no sign-in code captured")
	}
	if r := a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]string{"email": p.email, "code": code}, nil); r.status != http.StatusOK {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}

	// ---- 2. email change: both the new-address code and the
	//         current-address notice (internal/app/accounts/email_change_mail.go) ----
	newAddr := "selfhosted-mail-moved-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org"
	newCode, curCode := requestEmailChange(t, a, mb, key, p.email, newAddr)
	if r := a.at(t, "POST", a.appHost(), "/v1/me/email/verify", map[string]string{"code": newCode, "current_code": curCode}, key); r.status != http.StatusOK {
		t.Fatalf("email change verify: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, newAddr) })

	// ---- 3. the account-deletion notice (account_data.go's supportText
	//         path, the other SendNotice call site besides email-change) ----
	if r := a.at(t, "DELETE", a.appHost(), "/v1/me", map[string]string{"confirm": handle}, key); r.status != http.StatusNoContent {
		t.Fatalf("delete account: %d %s", r.status, r.body)
	}

	if hits := hostedDomainLeak(mb, nil); len(hits) > 0 {
		t.Errorf("self-hosted install named the hosted service in %d email(s):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}
