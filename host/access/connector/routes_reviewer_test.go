package connector

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

func TestReviewerSignInRateLimit(t *testing.T) {
	a := NewApp(t)
	reviewer := "reviewer-rl-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	a.Conn.EnableReviewerSignIn(reviewer, encoded)
	statuses := []int{}
	for i := 0; i < 12; i++ {
		statuses = append(statuses, a.ReviewerSignIn(t, reviewer, "wrong password, long enough", nil).Status)
	}
	if statuses[0] != 401 || statuses[9] != 401 || statuses[10] != 429 || statuses[11] != 429 {
		t.Fatalf("statuses %v: want ten 401s then 429", statuses)
	}
	// Once limited, even the right password waits: the limit is on attempts.
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusTooManyRequests {
		t.Fatalf("right password while limited: %d", r.Status)
	}
}

func TestReviewerAccountMustNotBeAdmin(t *testing.T) {
	a := NewApp(t)
	addr := "reviewer-admin-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountsrepo.CreateUser(context.Background(), a.Database, addr, key, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	a.Conn.EnableReviewerSignIn(addr, encoded)
	if r := a.ReviewerSignIn(t, addr, reviewerTestPassword, nil); r.Status != http.StatusForbidden || strings.Contains(string(r.Body), key) {
		t.Fatalf("admin reviewer account: %d %s", r.Status, r.Body)
	}
}

// A suspended reviewer account gets no new key.
func TestReviewerSignInSuspended(t *testing.T) {
	a := NewApp(t)
	reviewer := "reviewer-susp-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	a.Conn.EnableReviewerSignIn(reviewer, encoded)
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusOK {
		t.Fatalf("first sign-in: %d %s", r.Status, r.Body)
	}
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = $1`, reviewer).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.SetUserSuspended(context.Background(), a.Database, id, "test"); err != nil {
		t.Fatal(err)
	}
	r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil)
	if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" || strings.Contains(string(r.Body), "api_key") {
		t.Fatalf("suspended reviewer sign-in: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("keys after refused sign-in: %d %v", n, err)
	}
}
