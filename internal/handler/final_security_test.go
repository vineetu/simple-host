package handler

import (
	"context"
	"testing"

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
