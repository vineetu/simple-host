package db

import (
	"testing"
	"time"
)

func TestRetainedInternalKey(t *testing.T) {
	key, revoke, err := IssueInternalTeamKey("member", "team")
	if err != nil {
		t.Fatal(err)
	}
	defer internalKeys.Delete(key)
	expiry := time.Now().Add(16 * time.Minute)
	RetainInternalKey(key, expiry)
	revoke()
	cred, ok := lookupInternalCred(key)
	if !ok || cred.userID != "member" || cred.teamID != "team" || !cred.expires.Equal(expiry) {
		t.Fatal("retention lost identity or expiry")
	}
	cred.expires = time.Now().Add(-time.Second)
	internalKeys.Store(key, cred)
	if _, ok := lookupInternalCred(key); ok {
		t.Fatal("expired key accepted")
	}
	normal, done, err := IssueInternalKey("user")
	if err != nil {
		t.Fatal(err)
	}
	done()
	if _, ok := lookupInternalCred(normal); ok {
		t.Fatal("ordinary key retained")
	}
}
