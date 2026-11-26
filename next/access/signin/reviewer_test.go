package signin_test

import (
	"testing"

	"simplehost/access/signin"
	"simplehost/api"
	"simplehost/core/testkit"
)

// The plugin reviewer signs in with a password, only as the one account
// named, and only when both settings are there.
func TestReviewerPasswordSignIn(t *testing.T) {
	k := testkit.Start(t, signin.Mount, api.Mount)
	const at = "https://simple-host.app/v1/auth/reviewer"
	const email, password = "reviewer@example.com", "a-long-test-password-only"
	try := func(email, password string) testkit.Res {
		return testkit.Call(t, k.Browser(), "POST", at, map[string]string{"email": email, "password": password})
	}
	if r := try(email, password); r.Status != 404 {
		t.Fatalf("off: %d %s", r.Status, r.Body)
	}
	if _, err := signin.HashReviewPassword("short"); err == nil {
		t.Fatal("a short password was hashed")
	}
	hash, err := signin.HashReviewPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	k.App.Config.ReviewEmail, k.App.Config.ReviewPasswordHash = email, hash
	if r := try(email, password+"x"); r.Status != 401 {
		t.Fatalf("wrong password: %d %s", r.Status, r.Body)
	}
	other := k.Person(t, "other")
	if r := try(other.Email, password); r.Status != 401 {
		t.Fatalf("another account with the password: %d %s", r.Status, r.Body)
	}
	b := k.Browser()
	if r := testkit.Call(t, b, "POST", at, map[string]string{"email": " Reviewer@Example.com ", "password": password}); r.Status != 200 {
		t.Fatalf("reviewer: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", "https://simple-host.app/v1/me", nil); r.Status != 200 {
		t.Fatalf("signed in as the reviewer: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, k.Browser(), "POST", at, map[string]string{"email": email, "password": password}, "Origin", "https://evil.example"); r.Status != 403 {
		t.Fatalf("a request from another page: %d %s", r.Status, r.Body)
	}
	k.App.Config.ReviewPasswordHash = "pbkdf2-sha256$1000$AAAAAAAAAAAAAAAAAAAAAA$AAAA"
	if r := try(email, password); r.Status != 404 {
		t.Fatalf("a malformed hash half-enabled it: %d %s", r.Status, r.Body)
	}
}

// Guessing is limited like other sign-in.
func TestReviewerSignInIsLimited(t *testing.T) {
	k := testkit.Start(t, signin.Mount)
	hash, _ := signin.HashReviewPassword("a-long-test-password-only")
	k.App.Config.ReviewEmail, k.App.Config.ReviewPasswordHash = "reviewer@example.com", hash
	last := 0
	for i := 0; i < 12; i++ {
		last = testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/auth/reviewer",
			map[string]string{"email": "reviewer@example.com", "password": "wrong"}, "X-Real-IP", "10.9.9.9").Status
	}
	if last != 429 {
		t.Fatalf("after 12 guesses: %d", last)
	}
}

// Strangers' wrong tries do not lock the reviewer out.
func TestReviewerNotLockedOutByOthers(t *testing.T) {
	k := testkit.Start(t, signin.Mount, api.Mount)
	const at = "https://simple-host.app/v1/auth/reviewer"
	const email, password = "reviewer@example.com", "a-long-test-password-only"
	hash, _ := signin.HashReviewPassword(password)
	k.App.Config.ReviewEmail, k.App.Config.ReviewPasswordHash = email, hash
	for i := 0; i < 10; i++ {
		testkit.Call(t, k.Browser(), "POST", at, map[string]string{"email": email, "password": "wrong"}, "X-Real-IP", "10.9.9.9")
	}
	r := testkit.Call(t, k.Browser(), "POST", at, map[string]string{"email": email, "password": password}, "X-Real-IP", "10.8.8.8")
	if r.Status != 200 {
		t.Fatalf("the reviewer from another address: %d %s", r.Status, r.Body)
	}
}
