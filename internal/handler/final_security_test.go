package handler

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// v0.7.1 security fixes. Need DB_DSN (db/schema.sql applied).

func (a *privateApp) sessionCount(t *testing.T, uid string) int {
	t.Helper()
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id = $1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// M1: every "get the intruder out" lever also ends the account's sign-ins on
// sites, its own included (where they carry owner powers over saved data).
func TestLeversEndSiteSignIns(t *testing.T) {
	a := newPrivateApp(t)
	ctx := context.Background()
	const apex = "simple-host.test"
	p := a.newPerson(t, "levers")
	a.deploy(t, p, "shop")
	siteID := a.siteID(t, p, "shop")
	uid, h := a.userID(t, p)
	host := "shop." + h + "." + pcSiteDomain
	signIn := func() {
		a.session(t, p, siteID, host)
		if n := a.sessionCount(t, uid); n == 0 {
			t.Fatal("no session made")
		}
	}

	// Sign out everywhere (rotate).
	signIn()
	r := a.at(t, "POST", apex, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": p.key})
	if r.status != 200 {
		t.Fatalf("rotate: %d %s", r.status, r.body)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("rotate left %d site sign-ins", n)
	}
	p.key, _ = r.json(t)["api_key"].(string)

	// The admin's new key.
	signIn()
	newKey, _ := auth.GenerateAPIKey()
	if err := db.ReplaceAPIKeys(ctx, a.database, uid, newKey, "x"); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("admin new key left %d site sign-ins", n)
	}

	// Removing a linked Google sign-in.
	signIn()
	ident, err := db.InsertOAuthIdentity(ctx, a.database, uid, "google", "g-"+uid, p.email, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UnlinkSignInIdentity(ctx, a.database, uid, ident.ID); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("unlink left %d site sign-ins", n)
	}

	// Email change completion, then its undo.
	signIn()
	tx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	moved := "moved-" + p.email
	if err := db.ApplyEmailChange(ctx, tx, uid, p.email, moved, db.HashAPIKey(newKey), "undo-hash-"+uid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("email change left %d site sign-ins", n)
	}
	old := p.email
	p.email = moved
	signIn()
	if _, err := db.UndoEmailChange(ctx, a.database, "undo-hash-"+uid); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("undo left %d site sign-ins", n)
	}
	p.email = old

	// Suspension.
	signIn()
	if err := db.SetUserSuspended(ctx, a.database, uid, "abuse"); err != nil {
		t.Fatal(err)
	}
	if n := a.sessionCount(t, uid); n != 0 {
		t.Fatalf("suspension left %d site sign-ins", n)
	}
}

// M3: a site's rules see the account's current sign-in email, not a Google
// address linked before the account moved.
func TestVisitorEmailFollowsAccountEmail(t *testing.T) {
	a := newPrivateApp(t)
	ctx := context.Background()
	p := a.newPerson(t, "bobmoved")
	uid, _ := a.userID(t, p)
	if _, err := db.InsertOAuthIdentity(ctx, a.database, uid, "google", "g-"+uid, "bob-"+uid+"@acme.test", true); err != nil {
		t.Fatal(err)
	}
	got, err := visitorEmail(ctx, a.database, uid)
	if err != nil || got != p.email {
		t.Fatalf("visitorEmail = %q, %v; want the account email %q", got, err, p.email)
	}
	// An account whose name is not an address (event account) falls back to
	// its verified identity.
	if _, err := a.database.Exec(`UPDATE users SET username = $2 WHERE id = $1`, uid, "team-"+uid); err != nil {
		t.Fatal(err)
	}
	got, err = visitorEmail(ctx, a.database, uid)
	if err != nil || got != "bob-"+uid+"@acme.test" {
		t.Fatalf("event account visitorEmail = %q, %v", got, err)
	}
}

// L1: the service's own key names are refused as typed names, and a key's
// name never marks an account as an event account.
func TestReservedKeyNames(t *testing.T) {
	a := newPrivateApp(t)
	const apex = "simple-host.test"
	p := a.newPerson(t, "keynames")
	key := map[string]string{"X-API-Key": p.key}
	for _, n := range []string{"event account", " Event Account ", "DASHBOARD SIGN-IN", "agent sign-in", "reviewer sign-in", "replacement key"} {
		if r := a.at(t, "POST", apex, "/v1/me/keys", map[string]string{"name": n}, key); r.status != 400 {
			t.Fatalf("reserved name %q: %d %s", n, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", apex, "/v1/me/keys", map[string]string{"name": "event account 2"}, key); r.status != 200 && r.status != 201 {
		t.Fatalf("ordinary name: %d %s", r.status, r.body)
	}
	uid, _ := a.userID(t, p)
	// Even a key stored under the event name (an older row) does not make one.
	k, _ := auth.GenerateAPIKey()
	if err := db.AddAPIKey(context.Background(), a.database, uid, k, db.KeyNameEvent); err != nil {
		t.Fatal(err)
	}
	tgt, err := db.GetSignInAlertTarget(context.Background(), a.database, uid)
	if err != nil || tgt.Event {
		t.Fatalf("key name marked an event account: %+v %v", tgt, err)
	}
	if err := db.MarkEventAccount(context.Background(), a.database, uid); err != nil {
		t.Fatal(err)
	}
	if tgt, _ = db.GetSignInAlertTarget(context.Background(), a.database, uid); !tgt.Event {
		t.Fatal("flag not read")
	}
}

// L3: a handle that ever published keeps its old name as an alias on a
// change, even after every site row is gone, so nobody else can claim it.
func TestHandleChangeAfterPurgeKeepsAlias(t *testing.T) {
	a := newPersonApp(t, "serve")
	const apex = "simple-host.test"
	p := a.newPerson(t, "purged")
	a.deploy(t, p, "shop")
	uid, old := a.userID(t, p)
	if _, err := a.database.Exec(`DELETE FROM sites WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	nh := old + "-new"
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": nh}, map[string]string{"X-API-Key": p.key}); r.status != 200 {
		t.Fatalf("change: %d %s", r.status, r.body)
	}
	var owner string
	if err := a.database.QueryRow(`SELECT user_id::text FROM handle_aliases WHERE handle = $1`, old).Scan(&owner); err != nil || owner != uid {
		t.Fatalf("old handle not kept: %q %v", owner, err)
	}
	q := a.newPerson(t, "claimer")
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": old}, map[string]string{"X-API-Key": q.key}); r.status != 409 {
		t.Fatalf("stranger took the old handle: %d %s", r.status, r.body)
	}
}

// L5: an emailed link's GET without ?t= answers with the page that reads the
// fragment and posts it back; that post (peek=1) only shows the confirmation.
func TestFragmentLinkPage(t *testing.T) {
	a := newPrivateApp(t)
	for _, path := range []string{"/v1/idle/keep", "/v1/idle/restore", "/v1/data-notify/stop", "/v1/me/email/undo"} {
		r := a.at(t, "GET", "simple-host.test", path, nil, nil)
		body := string(r.body)
		if r.status != 200 || !strings.Contains(body, `action="`+path+`"`) || !strings.Contains(body, `name="peek" value="1"`) || !strings.Contains(body, "location.hash") {
			t.Fatalf("%s: %d %s", path, r.status, body)
		}
		if r.header.Get("Referrer-Policy") != "no-referrer" || r.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s headers: %v", path, r.header)
		}
		// A peek with an unknown token acts on nothing.
		if r := a.at(t, "POST", "simple-host.test", path, "peek=1&t="+strings.Repeat("0", 48), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); r.status != 404 {
			t.Fatalf("%s bad peek: %d", path, r.status)
		}
	}
}

// L6: the certificate check is rate-limited per address (RATE_LIMIT_TLS_ASK).
func TestTLSAskRateLimited(t *testing.T) {
	a := newPrivateApp(t)
	limited := false
	for i := 0; i < 90 && !limited; i++ {
		r := a.at(t, "GET", "simple-host.test", "/internal/tls-ask?domain=nobody-"+strings.Repeat("x", i%5)+".example", nil, nil)
		limited = r.status == 429
	}
	if !limited {
		t.Fatal("tls-ask never rate-limited")
	}
}

// I1: a taken-down site signs no visitor in (no code is emailed).
func TestTakenDownSiteSignsNobodyIn(t *testing.T) {
	a := newPersonApp(t, "serve")
	a.sites.SetSiteHosts("canonical", "")
	olive := a.newPerson(t, "tdown")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	shop := "shop." + oh + "." + pcSiteDomain
	shopID := a.siteID(t, olive, "shop")
	if err := db.SetSiteSuspended(context.Background(), a.database, shopID, "report"); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "ann@example.com"}, browser(shop, "")); r.status != 403 || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("visitor sign-in on a taken-down site: %d %s", r.status, r.body)
	}
}

// Owner decision 2026-09-27: impersonation-prone names are refused to new
// handles, new claimed names and (the platform-sounding subset) new sites;
// holders from before keep theirs.
func TestReservedNewNames(t *testing.T) {
	for _, n := range []string{"support", "info", "hello", "billing", "simple-host", "no-reply", "verify", "staging"} {
		if err := validateHandle(n); err == nil || err.Error() != "this name is reserved; pick another" {
			t.Errorf("handle %q: %v", n, err)
		}
	}
	for _, n := range []string{"support", "admin", "login", "simplehack"} {
		if validateSiteReserved(n) == nil {
			t.Errorf("site %q allowed", n)
		}
	}
	for _, n := range []string{"blog", "docs", "team", "test", "my-shop"} {
		if err := validateSiteReserved(n); err != nil {
			t.Errorf("site %q refused: %v", n, err)
		}
	}
	if err := validateHandle("olive-shop"); err != nil {
		t.Fatal(err)
	}
	// Grandfathered: an account that already holds such a handle keeps its
	// address (the serving check is the older, shorter list).
	if !handleAddressable("info") || !handleAddressable("hello") {
		t.Fatal("an existing holder of a newly reserved handle lost its address")
	}

	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "claimer")
	a.deploy(t, p, "shop")
	key := map[string]string{"X-API-Key": p.key}
	for _, n := range []string{"support", "hello"} {
		r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": n + "." + pcSiteDomain}, key)
		if r.status != 400 || r.json(t)["code"] != "name_reserved" {
			t.Fatalf("claim %s: %d %s", n, r.status, r.body)
		}
	}
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/me", map[string]string{"handle": "security"}, key); r.status != 409 || r.json(t)["code"] != "handle_reserved" || !strings.Contains(string(r.body), "That address is reserved: security."+pcSiteDomain) {
		t.Fatalf("handle change: %d %s", r.status, r.body)
	}
	// A sign-up whose address would make a reserved handle gets another.
	uid, _ := a.userID(t, p)
	if _, err := a.database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	assignHandle(context.Background(), a.database, uid, "support@example.com")
	var h string
	if err := a.database.QueryRow(`SELECT handle FROM users WHERE id = $1`, uid).Scan(&h); err != nil || h == "support" || !strings.HasPrefix(h, "support-") {
		t.Fatalf("assigned handle %q %v", h, err)
	}
}

// A sign-in may not name its key "event account" either (the sign-in pages'
// own "dashboard sign-in" stays allowed).
func TestSignInRefusesEventKeyName(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "evname")
	lt, _ := auth.GenerateAPIKey()
	if err := db.CreateAuthToken(context.Background(), a.database, ann.email, "555555", lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	r := a.do(t, http.MethodPost, "/v1/auth/verify", jsonBody(map[string]string{"email": ann.email, "code": "555555", "name": "Event Account"}), map[string]string{"Content-Type": "application/json"})
	if r.status != http.StatusBadRequest {
		t.Fatalf("verify with the event name: %d %s", r.status, r.body)
	}
}
