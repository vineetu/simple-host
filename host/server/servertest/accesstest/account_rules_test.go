package accesstest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// Every "get the intruder out" lever also ends the account's sign-ins on
// sites, its own included.
func TestLeversEndSiteSignIns(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	p := a.NewPerson(t, "levers")
	siteID := a.Deploy(t, p, "shop")
	host := p.SiteHost("shop")
	svc := accounts.New(a.Database)
	ended := func(lever string, pull func()) {
		t.Helper()
		a.Session(t, p, siteID, host)
		pull()
		var n int
		if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s left %d site sign-ins (%v)", lever, n, err)
		}
	}
	ended("rotate", func() {
		r := a.At(t, "POST", rt.SiteDomain, "/v1/me/api-key/rotate", nil, p.Header())
		if r.Status != 200 {
			t.Fatalf("rotate: %d %s", r.Status, r.Body)
		}
		p.Key, _ = r.JSON(t)["api_key"].(string)
	})
	newKey, _ := auth.GenerateAPIKey()
	ended("admin new key", func() {
		if err := accountsrepo.ReplaceAPIKeys(ctx, a.Database, p.ID, newKey, "x"); err != nil {
			t.Fatal(err)
		}
	})
	ended("unlink", func() {
		ident, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, p.ID, "google", "g-"+p.ID, p.Email, true)
		if err == nil {
			err = svc.UnlinkSignInIdentity(ctx, p.ID, ident.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	})
	moved := "moved-" + p.Email
	ended("email change", func() {
		tx, err := a.Database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := accountsrepo.ApplyEmailChange(ctx, tx, p.ID, p.Email, moved, accountsrepo.HashAPIKey(newKey), "undo-hash-"+p.ID, 7*24*time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	ended("email change undo", func() {
		if _, err := svc.UndoEmailChange(ctx, "undo-hash-"+p.ID); err != nil {
			t.Fatal(err)
		}
	})
	ended("suspension", func() {
		if err := accountsrepo.SetUserSuspended(ctx, a.Database, p.ID, "abuse"); err != nil {
			t.Fatal(err)
		}
	})
}

// A site's rules see the account's current sign-in email; a self-signup
// password account's typed login is never trusted as a verified address
// (pre-account-hijacking fix, 2026-11-05), an admin-made one is.
func TestVisitorEmail(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	email := func(id string) string {
		t.Helper()
		got, err := accounts.VisitorEmail(ctx, a.Database, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	p := a.NewPerson(t, "bobmoved")
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, p.ID, "google", "g-"+p.ID, "bob-"+p.ID+"@acme.test", true); err != nil {
		t.Fatal(err)
	}
	if got := email(p.ID); got != p.Email {
		t.Fatalf("visitor email %q, want the account email %q", got, p.Email)
	}
	// An account whose name is not an address falls back to its verified identity.
	if _, err := a.Database.Exec(`UPDATE users SET username = $2 WHERE id = $1`, p.ID, "team-"+p.ID); err != nil {
		t.Fatal(err)
	}
	if got := email(p.ID); got != "bob-"+p.ID+"@acme.test" {
		t.Fatalf("event account visitor email %q", got)
	}

	svc := accounts.New(a.Database)
	assign := func(ctx context.Context, q sqlkit.Querier, id, email string, random bool) {
		accounts.AssignHandle(ctx, q, id, email, random)
	}
	address := fmt.Sprintf("victim-%d@example.com", time.Now().UnixNano())
	self, _, err := svc.CreatePasswordAccount(ctx, address, "squatter password", false, false, assign)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id=$1`, self.ID) })
	if got := email(self.ID); strings.Contains(got, "@") {
		t.Fatalf("self-signup password login trusted as an email: %q", got)
	}
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, self.ID, "google", "g-"+self.ID, address, true); err != nil {
		t.Fatal(err)
	}
	if got := email(self.ID); got != address {
		t.Fatalf("proved address not used: %q", got)
	}
	adminAddr := fmt.Sprintf("admin-made-%d@example.com", time.Now().UnixNano())
	made, _, err := svc.CreatePasswordAccount(ctx, adminAddr, "", true, false, assign)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id=$1`, made.ID) })
	if got := email(made.ID); got != adminAddr {
		t.Fatalf("admin-made account's address not trusted: %q", got)
	}
}

// The service's own key names are refused as typed names, at key creation
// and at sign-in.
func TestReservedKeyNames(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "keynames")
	for _, n := range []string{"event account", " Event Account ", "DASHBOARD SIGN-IN", "reviewer sign-in"} {
		if r := a.At(t, "POST", rt.SiteDomain, "/v1/me/keys", map[string]string{"name": n}, p.Header()); r.Status != 400 {
			t.Fatalf("reserved name %q: %d %s", n, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/me/keys", map[string]string{"name": "event account 2"}, p.Header()); r.Status/100 != 2 {
		t.Fatalf("ordinary name: %d %s", r.Status, r.Body)
	}
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, p.Email, "555555", lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	if r := a.Do(t, http.MethodPost, "/v1/auth/verify", map[string]string{"email": p.Email, "code": "555555", "name": "Event Account"}, map[string]string{"Content-Type": "application/json"}); r.Status != http.StatusBadRequest {
		t.Fatalf("sign-in with the event key name: %d %s", r.Status, r.Body)
	}
}

// A handle that ever published keeps its old name as an alias on a change,
// even after every site row is gone; reserved handles are refused.
func TestHandleChangeKeepsAliasAndRefusesReserved(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "purged")
	a.Deploy(t, p, "shop")
	if _, err := a.Database.Exec(`DELETE FROM sites WHERE user_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "PATCH", rt.SiteDomain, "/v1/me", map[string]string{"handle": p.Handle + "-new"}, p.Header()); r.Status != 200 {
		t.Fatalf("change: %d %s", r.Status, r.Body)
	}
	var owner string
	if err := a.Database.QueryRow(`SELECT user_id::text FROM handle_aliases WHERE handle = $1`, p.Handle).Scan(&owner); err != nil || owner != p.ID {
		t.Fatalf("old handle not kept: %q %v", owner, err)
	}
	q := a.NewPerson(t, "claimer")
	if r := a.At(t, "PATCH", rt.SiteDomain, "/v1/me", map[string]string{"handle": p.Handle}, q.Header()); r.Status != 409 {
		t.Fatalf("stranger took the old handle: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", rt.SiteDomain, "/v1/me", map[string]string{"handle": "security"}, q.Header()); r.Status != 409 || r.Code() != "handle_reserved" {
		t.Fatalf("reserved handle: %d %s", r.Status, r.Body)
	}
}

// An establish code used on the wrong host is burned; the right host works once.
func TestEstablishTokenWrongHostBurns(t *testing.T) {
	a := NewApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	siteID := a.Deploy(t, olive, "clay")
	a.Deploy(t, olive, "pots")
	dom, other := olive.SiteHost("clay"), olive.SiteHost("pots")
	ctx := context.Background()
	now := time.Now()
	issue := func(sidByte, once string) {
		sid, _ := hex.DecodeString(strings.Repeat(sidByte, 32))
		if _, err := accountsrepo.InsertVisitorSession(ctx, a.Database, sid, vic.ID, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = accountsrepo.DeleteVisitorSession(ctx, a.Database, sid) })
		if err := accountsrepo.InsertEstablishToken(ctx, a.Database, once, sid, dom, "https://"+dom+"/", NonceHash(TestNonce), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	establish := func(host, once string) Resp {
		return a.At(t, "GET", host, "/v1/visitor/establish?once="+once, nil, NonceCookie(TestNonce))
	}
	burn := "burn-" + vic.ID
	issue("cd", burn)
	if r := establish(other, burn); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong host accepted: %d %v", r.Status, r.Header.Values("Set-Cookie"))
	}
	if r := establish(dom, burn); r.Status != http.StatusBadRequest {
		t.Fatalf("burned code replayed on the right host: %d", r.Status)
	}
	good := "good-" + vic.ID
	issue("ef", good)
	if r := establish(dom, good); r.Status != http.StatusFound {
		t.Fatalf("normal establish: %d %s", r.Status, r.Body)
	}
	if r := establish(dom, good); r.Status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", r.Status)
	}
}

// After an email change, sign-in alerts go to the new address only.
func TestSignInAlertFollowsEmailChange(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "switcher")
	newAddr := Uniq("switched") + "@example.org"
	code, cur := RequestEmailChange(t, a, mb, p.Header(), p.Email, newAddr)
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, p.Header()); r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, newAddr) })
	before := len(mb.To(p.Email))
	SignInWith(t, a, mb, newAddr, UaMacChrome)
	if n := len(mb.To(newAddr)); n == 0 {
		t.Fatal("no alert to the new address")
	}
	if n := len(mb.To(p.Email)); n != before {
		t.Fatalf("old address got %d more notice(s) after the change", n-before)
	}
}

// VISITOR_SESSION_DAYS and VISITOR_SESSION_IDLE_DAYS set a visitor
// sign-in's lifetime, and touching it slides only the idle expiry.
func TestVisitorSessionFollowsSetting(t *testing.T) {
	l := WithLimits(t, map[string]string{"VISITOR_SESSION_DAYS": "60", "VISITOR_SESSION_IDLE_DAYS": "21"})
	a := NewApp(t, edition.Default(l))
	a.WithOAuth(t)
	olive := a.NewPerson(t, "sessions")
	a.Deploy(t, olive, "shop")
	host := olive.SiteHost("shop")
	state, nonce := a.SignInStart(t, host, "https://"+host+"/")
	loc := a.SignInCallback(t, state, "visitor", host)
	r := a.At(t, "GET", host, loc.RequestURI(), nil, NonceCookie(nonce))
	sess := ResponseCookie(t, r, accesshttp.VisitorCookieHost)
	id, _ := hex.DecodeString(sess)
	near := func(got time.Time, days int) bool {
		d := got.Sub(time.Now().Add(time.Duration(days) * 24 * time.Hour))
		return d > -time.Minute && d < time.Minute
	}
	var exp, idle time.Time
	if err := a.Database.QueryRow(`SELECT expires_at, idle_expires_at FROM visitor_sessions WHERE id = $1`, id).Scan(&exp, &idle); err != nil {
		t.Fatal(err)
	}
	if !near(exp, 60) || !near(idle, 21) {
		t.Fatalf("session expires %v, idle %v; want 60 and 21 days out", exp, idle)
	}
	if _, err := a.Database.Exec(`UPDATE visitor_sessions SET idle_expires_at = now() + interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.TouchVisitorSession(context.Background(), a.Database, id, l.VisitorSessionIdle); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`SELECT idle_expires_at FROM visitor_sessions WHERE id = $1`, id).Scan(&idle); err != nil || !near(idle, 21) {
		t.Fatalf("slid idle expiry %v (%v), want 21 days out", idle, err)
	}
}
