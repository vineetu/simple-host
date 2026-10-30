package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestInternalVisitorPassLifecycleAndNoOwner(t *testing.T) {
	pass := InternalVisitorPass{UserID: "person", SiteID: "one", Host: "one.example", AppName: "ChatGPT"}
	key, revoke, err := IssueInternalVisitorKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	if got, ok := LookupInternalVisitorKey(key); !ok || got != pass {
		t.Fatalf("lookup: %+v %v", got, ok)
	}
	if _, ok := lookupInternalKey(key); ok {
		t.Fatal("visitor pass resolved as owner key")
	}
	if _, _, ok := lookupInternalTeamKey(key); ok {
		t.Fatal("visitor pass resolved as team key")
	}
	// Nil DB proves rejection happens before loading any account identity.
	if _, err := GetUserByAPIKey(context.Background(), nil, key); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owner lookup: %v", err)
	}
	revoke()
	if _, ok := LookupInternalVisitorKey(key); ok {
		t.Fatal("revoked pass accepted")
	}
	key, revoke, err = IssueInternalVisitorKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	cred, _ := lookupInternalCred(key)
	cred.expires = time.Now().Add(-time.Second)
	internalKeys.Store(key, cred)
	if _, ok := LookupInternalVisitorKey(key); ok {
		t.Fatal("expired pass accepted")
	}
	owner, undo, err := IssueInternalKey("person")
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	if _, ok := LookupInternalVisitorKey(owner); ok {
		t.Fatal("owner credential accepted as visitor pass")
	}
}
