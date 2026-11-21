package accesstest

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/web"
)

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
	newCode, curCode := RequestEmailChange(t, a, mb, key, p.Email, newAddr)
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
