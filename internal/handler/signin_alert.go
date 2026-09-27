package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

// SignInAlerts emails the owner after each successful sign-in (emailed code or
// link, Google) and each AI app connected through the consent screen: when,
// from which browser or app, and where "Sign out everywhere" is. Never the IP
// or a location. At most one per account, browser/app and UTC day; none for
// event accounts, the plugin reviewer account or an account that turned them
// off. API key calls never send one.
type SignInAlerts struct {
	database      *sql.DB
	mailer        email.Sender
	publicBaseURL string
	reviewerEmail string
	// run sends in the background so a sign-in never waits on the mail
	// provider; tests make it synchronous.
	run func(func())
	now func() time.Time
}

func newSignInAlerts(database *sql.DB, mailer email.Sender, publicBaseURL string) *SignInAlerts {
	return &SignInAlerts{
		database:      database,
		mailer:        mailer,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		run:           func(f func()) { go f() },
		now:           time.Now,
	}
}

// Alert sends the alert for a sign-in by userID from userAgent. app names the
// AI app just connected, or is empty for a plain sign-in.
func (a *SignInAlerts) Alert(userID, userAgent, app string) {
	if a == nil || userID == "" {
		return
	}
	mailer, ok := a.mailer.(noticeSender)
	if !ok {
		return
	}
	at := a.now().UTC()
	summary := summarizeUserAgent(userAgent)
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, err := db.GetSignInAlertTarget(ctx, a.database, userID)
		if err != nil {
			log.Printf("sign-in alert user_id=%s: %v", userID, err)
			return
		}
		if !t.On || t.Event || !strings.Contains(t.Email, "@") ||
			(a.reviewerEmail != "" && strings.EqualFold(t.Email, a.reviewerEmail)) {
			return
		}
		key := summary
		if app != "" {
			key = summary + " · " + app
		}
		first, err := db.ClaimSignInAlert(ctx, a.database, userID, key)
		if err != nil || !first {
			if err != nil {
				log.Printf("sign-in alert user_id=%s: claim: %v", userID, err)
			}
			return
		}
		subject, text := signInAlertText(t.Email, a.ownerAppURL(t.Handle), summary, app, at)
		if err := mailer.SendNotice(t.Email, subject, text); err != nil {
			log.Printf("sign-in alert user_id=%s: send: %v", userID, err)
		}
	})
}

// ownerAppURL is where the account's "Sign out everywhere" and the alert
// switch are: the owner app for an account with an address, else /dashboard.
func (a *SignInAlerts) ownerAppURL(handle string) string {
	if handle != "" {
		return a.publicBaseURL + "/" + handle
	}
	return a.publicBaseURL + "/dashboard"
}

func signInAlertText(to, link, summary, app string, at time.Time) (string, string) {
	when := at.Format("2 Jan 2006, 15:04") + " UTC"
	subject := "New sign-in to your Simple Host account"
	what := "Your Simple Host account (" + to + ") was signed in to."
	if app != "" {
		// The name is self-declared by the app, so it stays out of the
		// subject and is quoted in the body.
		subject = "An app was connected to your Simple Host account"
		what = "An app called \"" + app + "\" was connected to your Simple Host account (" + to + "). It can now publish and manage your sites for you."
	}
	text := fmt.Sprintf(`%s

When: %s
From: %s

If this was you, there is nothing to do.

If it wasn't, open %s and press "Sign out everywhere": every key stops working and every connected app is disconnected. Then write to support@simple-host.app.

You can turn these emails off on the same page.

Simple Host
`, what, when, summary, link)
	return subject, text
}

var (
	uaEdge    = regexp.MustCompile(`\bEdg(e|A|iOS)?/`)
	uaOpera   = regexp.MustCompile(`\b(OPR|Opera)/`)
	uaFirefox = regexp.MustCompile(`\b(Firefox|FxiOS)/`)
	uaChrome  = regexp.MustCompile(`\b(Chrome|CriOS)/`)
	uaSafari  = regexp.MustCompile(`\bSafari/`)
	uaProduct = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,39}`)
)

// summarizeUserAgent turns a User-Agent into a few plain words ("Chrome on
// macOS", "curl"). Only the browser and system families are kept, never
// versions, so it identifies nobody and dedupes cleanly.
func summarizeUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "an unknown browser or app"
	}
	browser := ""
	switch {
	case uaEdge.MatchString(ua):
		browser = "Edge"
	case uaOpera.MatchString(ua):
		browser = "Opera"
	case uaFirefox.MatchString(ua):
		browser = "Firefox"
	case uaChrome.MatchString(ua):
		browser = "Chrome"
	case uaSafari.MatchString(ua):
		browser = "Safari"
	}
	system := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		system = "iPhone"
	case strings.Contains(ua, "iPad"):
		system = "iPad"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "CrOS"):
		system = "ChromeOS"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		system = "macOS"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "" && strings.HasPrefix(ua, "Mozilla/"):
		return "a browser on " + system
	}
	// Not a browser: an agent, script or app. Its product name is enough.
	if name := uaProduct.FindString(ua); name != "" && name != "Mozilla" {
		return name
	}
	return "an unknown browser or app"
}

// maskEmail shows enough of an address to recognise it: n***@example.com.
func maskEmail(address string) string {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}
