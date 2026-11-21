package servertest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// M1: every "get the intruder out" lever also ends the account's sign-ins on
// sites, its own included (where they carry owner powers over saved data).
func TestLeversEndSiteSignIns(t *testing.T) {
	a := NewPrivateApp(t)
	ctx := context.Background()
	const apex = "simple-host.test"
	p := a.NewPerson(t, "levers")
	a.Deploy(t, p, "shop")
	siteID := a.SiteID(t, p, "shop")
	uid, h := a.UserID(t, p)
	host := "shop." + h + "." + PcSiteDomain
	signIn := func() {
		a.Session(t, p, siteID, host)
		if n := a.sessionCount(t, uid); n == 0 {
			t.Fatal("no session made")
		}
	}

	// Sign out everywhere (rotate).
	signIn()
	r := a.At(t, "POST", apex, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": p.Key})
	if r.Status != 200 {
		t.Fatalf("rotate: %d %s", r.Status, r.Body)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("rotate left %d site sign-ins", n)
	}
	p.Key, _ = r.JSON(t)["api_key"].(string)

	// The admin's new key.
	signIn()
	newKey, _ := auth.GenerateAPIKey()
	if err := accountsrepo.ReplaceAPIKeys(ctx, a.Database, uid, newKey, "x"); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("admin new key left %d site sign-ins", n)
	}

	// Removing a linked Google sign-in.
	signIn()
	ident, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, uid, "google", "g-"+uid, p.Email, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := accountapp.New(a.Database).UnlinkSignInIdentity(ctx, uid, ident.ID); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("unlink left %d site sign-ins", n)
	}

	// Email change completion, then its undo.
	signIn()
	tx, err := a.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	moved := "moved-" + p.Email
	if err := accountsrepo.ApplyEmailChange(ctx, tx, uid, p.Email, moved, accountsrepo.HashAPIKey(newKey), "undo-hash-"+uid, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("email change left %d site sign-ins", n)
	}
	old := p.Email
	p.Email = moved
	signIn()
	if _, err := accountapp.New(a.Database).UndoEmailChange(ctx, "undo-hash-"+uid); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("undo left %d site sign-ins", n)
	}
	p.Email = old

	// Suspension.
	signIn()
	if err := accountsrepo.SetUserSuspended(ctx, a.Database, uid, "abuse"); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("suspension left %d site sign-ins", n)
	}
}

// M3: a site's rules see the account's current sign-in email, not a Google
// address linked before the account moved.
func TestVisitorEmailFollowsAccountEmail(t *testing.T) {
	a := NewPrivateApp(t)
	ctx := context.Background()
	p := a.NewPerson(t, "bobmoved")
	uid, _ := a.UserID(t, p)
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, uid, "google", "g-"+uid, "bob-"+uid+"@acme.test", true); err != nil {
		t.Fatal(err)
	}
	got, err := accesshttp.VisitorEmail(ctx, a.Database, uid)
	if err != nil || got != p.Email {
		t.Fatalf("visitorEmail = %q, %v; want the account email %q", got, err, p.Email)
	}
	// An account whose name is not an address (event account) falls back to
	// its verified identity.
	if _, err := a.Database.Exec(`UPDATE users SET username = $2 WHERE id = $1`, uid, "team-"+uid); err != nil {
		t.Fatal(err)
	}
	got, err = accesshttp.VisitorEmail(ctx, a.Database, uid)
	if err != nil || got != "bob-"+uid+"@acme.test" {
		t.Fatalf("event account visitorEmail = %q, %v", got, err)
	}
}

// Pre-account-hijacking fix, visitorEmail side (2026-11-05): a self-signup
// password account's username is never trusted as a verified address, even
// one shaped like an email -- the person who typed it never proved they
// control it. viewerAllowed and every other gate that calls visitorEmail
// (savers, visitor sessions, connector/MCP visitor tools) would otherwise
// let such an account pass as the real owner of that address, e.g. on a
// named-viewers site that lists the squatted email.
func TestVisitorEmailRefusesSelfSignupPasswordAccount(t *testing.T) {
	a := NewPrivateApp(t)
	ctx := context.Background()
	assign := func(ctx context.Context, q accountapp.AttributionStore, id, email string, random bool) {
		accounts.AssignHandle(ctx, q, id, email, random)
	}
	svc := accountapp.New(a.Database)

	address := fmt.Sprintf("victim-%d@example.com", time.Now().UnixNano())

	// Self sign-up with someone else's address as the login: never proved
	// control of it. Its users.username is a synthetic value now
	// (CreatePasswordAccount), so visitorEmail never even sees the typed
	// address -- confirmed here as defence in depth, same as its own
	// SelfSignupPasswordAccount check.
	selfUser, _, err := svc.CreatePasswordAccount(ctx, address, "squatter password", false, false, assign)
	if err != nil {
		t.Fatalf("self-signup password account: %v", err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id=$1`, selfUser.ID) })
	if got, err := accesshttp.VisitorEmail(ctx, a.Database, selfUser.ID); err != nil || got == address || strings.Contains(got, "@") {
		t.Fatalf("self-signup password account's typed login trusted as a verified email: got %q, err %v", got, err)
	}

	// The same account, later proving that address through Google: now it
	// has a real verified email to fall back to.
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, selfUser.ID, "google", "g-"+selfUser.ID, address, true); err != nil {
		t.Fatal(err)
	}
	if got, err := accesshttp.VisitorEmail(ctx, a.Database, selfUser.ID); err != nil || got != address {
		t.Fatalf("linked OAuth identity not used as the verified email: got %q, err %v", got, err)
	}

	// An admin-created (temporary) password account under an email keeps
	// the old behaviour: the admin vouched for that address.
	adminAddress := fmt.Sprintf("admin-made-%d@example.com", time.Now().UnixNano())
	adminUser, _, err := svc.CreatePasswordAccount(ctx, adminAddress, "", true, false, assign)
	if err != nil {
		t.Fatalf("admin-created password account: %v", err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id=$1`, adminUser.ID) })
	if got, err := accesshttp.VisitorEmail(ctx, a.Database, adminUser.ID); err != nil || got != adminAddress {
		t.Fatalf("admin-created password account's address not trusted: got %q, err %v", got, err)
	}
}

// L1: the service's own key names are refused as typed names, and a key's
// name never marks an account as an event account.
func TestReservedKeyNames(t *testing.T) {
	a := NewPrivateApp(t)
	const apex = "simple-host.test"
	p := a.NewPerson(t, "keynames")
	key := map[string]string{"X-API-Key": p.Key}
	for _, n := range []string{"event account", " Event Account ", "DASHBOARD SIGN-IN", "agent sign-in", "reviewer sign-in", "replacement key"} {
		if r := a.At(t, "POST", apex, "/v1/me/keys", map[string]string{"name": n}, key); r.Status != 400 {
			t.Fatalf("reserved name %q: %d %s", n, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", apex, "/v1/me/keys", map[string]string{"name": "event account 2"}, key); r.Status != 200 && r.Status != 201 {
		t.Fatalf("ordinary name: %d %s", r.Status, r.Body)
	}
	uid, _ := a.UserID(t, p)
	// Even a key stored under the event name (an older row) does not make one.
	k, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, uid, k, model.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	tgt, err := accountsrepo.GetSignInAlertTarget(context.Background(), a.Database, uid)
	if err != nil || tgt.Event {
		t.Fatalf("key name marked an event account: %+v %v", tgt, err)
	}
	if err := accountsrepo.MarkEventAccount(context.Background(), a.Database, uid); err != nil {
		t.Fatal(err)
	}
	if tgt, _ = accountsrepo.GetSignInAlertTarget(context.Background(), a.Database, uid); !tgt.Event {
		t.Fatal("flag not read")
	}
}

// L3: a handle that ever published keeps its old name as an alias on a
// change, even after every site row is gone, so nobody else can claim it.
func TestHandleChangeAfterPurgeKeepsAlias(t *testing.T) {
	a := NewPersonApp(t, "serve")
	const apex = "simple-host.test"
	p := a.NewPerson(t, "purged")
	a.Deploy(t, p, "shop")
	uid, old := a.UserID(t, p)
	if _, err := a.Database.Exec(`DELETE FROM sites WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	nh := old + "-new"
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": nh}, map[string]string{"X-API-Key": p.Key}); r.Status != 200 {
		t.Fatalf("change: %d %s", r.Status, r.Body)
	}
	var owner string
	if err := a.Database.QueryRow(`SELECT user_id::text FROM handle_aliases WHERE handle = $1`, old).Scan(&owner); err != nil || owner != uid {
		t.Fatalf("old handle not kept: %q %v", owner, err)
	}
	q := a.NewPerson(t, "claimer")
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": old}, map[string]string{"X-API-Key": q.Key}); r.Status != 409 {
		t.Fatalf("stranger took the old handle: %d %s", r.Status, r.Body)
	}
}

// L5: an emailed link's GET without ?t= answers with the page that reads the
// fragment and posts it back; that post (peek=1) only shows the confirmation.
func TestFragmentLinkPage(t *testing.T) {
	a := NewPrivateApp(t)
	for _, path := range []string{"/v1/me/email/undo"} {
		r := a.At(t, "GET", "simple-host.test", path, nil, nil)
		body := string(r.Body)
		if r.Status != 200 || !strings.Contains(body, `action="`+path+`"`) || !strings.Contains(body, `name="peek" value="1"`) || !strings.Contains(body, "location.hash") {
			t.Fatalf("%s: %d %s", path, r.Status, body)
		}
		if r.Header.Get("Referrer-Policy") != "no-referrer" || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s headers: %v", path, r.Header)
		}
		// A peek with an unknown token acts on nothing.
		if r := a.At(t, "POST", "simple-host.test", path, "peek=1&t="+strings.Repeat("0", 48), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); r.Status != 404 {
			t.Fatalf("%s bad peek: %d", path, r.Status)
		}
	}
}

// L6: the certificate check is rate-limited per address (RATE_LIMIT_TLS_ASK).
func TestTLSAskRateLimited(t *testing.T) {
	a := NewPrivateApp(t)
	limited := false
	for i := 0; i < 90 && !limited; i++ {
		r := a.At(t, "GET", "simple-host.test", "/internal/tls-ask?domain=nobody-"+strings.Repeat("x", i%5)+".example", nil, nil)
		limited = r.Status == 429
	}
	if !limited {
		t.Fatal("tls-ask never rate-limited")
	}
}

// I1: a taken-down site signs no visitor in (no code is emailed).
func TestTakenDownSiteSignsNobodyIn(t *testing.T) {
	a := NewPersonApp(t, "serve")
	a.Sites.Hosts.SetSiteHosts("canonical", "")
	olive := a.NewPerson(t, "tdown")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	shop := "shop." + oh + "." + PcSiteDomain
	shopID := a.SiteID(t, olive, "shop")
	if err := sitesrepo.SetSiteSuspended(context.Background(), a.Database, shopID, "report"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "ann@example.com"}, Browser(shop, "")); r.Status != 403 || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("visitor sign-in on a taken-down site: %d %s", r.Status, r.Body)
	}
}

// Owner decision 2026-09-27: impersonation-prone names are refused to new
// handles, new claimed names and (the platform-sounding subset) new sites;
// holders from before keep theirs.
func TestReservedNewNames(t *testing.T) {
	for _, n := range []string{"support", "info", "hello", "billing", "simple-host", "no-reply", "verify", "staging"} {
		if err := accesshttp.ValidateHandle(n); err == nil || err.Error() != "this name is reserved; pick another" {
			t.Errorf("handle %q: %v", n, err)
		}
	}
	if err := accesshttp.ValidateHandle("olive-shop"); err != nil {
		t.Fatal(err)
	}
	// Grandfathered: an account that already holds such a handle keeps its
	// address (the serving check is the older, shorter list).
	if !sites.HandleAddressable("info") || !sites.HandleAddressable("hello") {
		t.Fatal("an existing holder of a newly reserved handle lost its address")
	}

	a := NewPersonApp(t, "serve")
	p := a.NewPerson(t, "claimer")
	a.Deploy(t, p, "shop")
	key := map[string]string{"X-API-Key": p.Key}
	for _, n := range []string{"support", "hello"} {
		r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": n + "." + PcSiteDomain}, key)
		if r.Status != 400 || r.JSON(t)["code"] != "name_reserved" {
			t.Fatalf("claim %s: %d %s", n, r.Status, r.Body)
		}
	}
	if r := a.At(t, "PATCH", PcSiteDomain, "/v1/me", map[string]string{"handle": "security"}, key); r.Status != 409 || r.JSON(t)["code"] != "handle_reserved" || !strings.Contains(string(r.Body), "That address is reserved: security."+PcSiteDomain) {
		t.Fatalf("handle change: %d %s", r.Status, r.Body)
	}
	// A sign-up whose address would make a reserved handle gets another.
	uid, _ := a.UserID(t, p)
	if _, err := a.Database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	accounts.AssignHandle(context.Background(), a.Database, uid, "support@example.com", false)
	var h string
	if err := a.Database.QueryRow(`SELECT handle FROM users WHERE id = $1`, uid).Scan(&h); err != nil || h == "support" || !strings.HasPrefix(h, "support-") {
		t.Fatalf("assigned handle %q %v", h, err)
	}
}

// A sign-in may not name its key "event account" either (the sign-in pages'
// own "dashboard sign-in" stays allowed).
func TestSignInRefusesEventKeyName(t *testing.T) {
	a := NewConnectorApp(t)
	ann := a.NewPerson(t, "evname")
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, ann.Email, "555555", lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	r := a.Do(t, http.MethodPost, "/v1/auth/verify", JSONBody(map[string]string{"email": ann.Email, "code": "555555", "name": "Event Account"}), map[string]string{"Content-Type": "application/json"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("verify with the event name: %d %s", r.Status, r.Body)
	}
}
