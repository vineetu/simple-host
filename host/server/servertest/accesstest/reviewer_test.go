package accesstest

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

const reviewerTestPassword = "correct horse battery staple"

func TestReviewerSignInEndToEnd(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)

	// Off by default: no endpoint, no option on the page.
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusNotFound {
		t.Fatalf("disabled reviewer sign-in answered %d", r.Status)
	}
	if _, shown := a.AuthorizeData(t, clientID)["reviewer_signin"]; shown {
		t.Fatal("consent page offers reviewer sign-in while it is off")
	}

	if !a.Conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable")
	}
	if a.AuthorizeData(t, clientID)["reviewer_signin"] != true {
		t.Fatal("consent page does not offer reviewer sign-in")
	}

	// Another person's email with the reviewer password opens nothing, and
	// the reviewer's email with any other password opens nothing.
	other := a.NewPerson(t, "someone")
	for _, c := range [][2]string{{reviewer, "wrong password, long enough"}, {other.Email, reviewerTestPassword}, {"", reviewerTestPassword}, {reviewer, ""}} {
		if r := a.ReviewerSignIn(t, c[0], c[1], nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "api_key") {
			t.Fatalf("%q/%q: %d %s", c[0], c[1], r.Status, r.Body)
		}
	}
	// Cross-site posts are refused before any password check.
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, map[string]string{"Origin": "https://evil.example"}); r.Status != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", r.Status)
	}

	// The right pair signs in as an ordinary account, created on first use.
	r := a.ReviewerSignIn(t, "  "+strings.ToUpper(reviewer)+" ", reviewerTestPassword, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("reviewer sign-in: %d %s", r.Status, r.Body)
	}
	key := r.JSON(t)["api_key"].(string)
	me := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": key}).JSON(t)
	if me["username"] != reviewer || me["is_admin"] != false || me["handle"] == "" {
		t.Fatalf("reviewer account: %v", me)
	}

	// And it connects an app end to end, and publishes through it.
	access := a.Connect(t, servertest.Person{Email: reviewer, Key: key}, clientID, servertest.TestRedirect)["access_token"].(string)
	text, s, isErr := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{
		"site": "reviewer-demo", "files": map[string]any{"index.html": "<h1>demo</h1>"},
	}}))
	if isErr || !strings.Contains(s["url"].(string), "/reviewer-demo/") {
		t.Fatalf("create_site as the reviewer: %s", text)
	}

	// A second sign-in returns the same account, not a new one. Keys are
	// stored hashed, so it hands out a new key and the first keeps working.
	again := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil)
	if again.Status != http.StatusOK {
		t.Fatalf("second sign-in: %d", again.Status)
	}
	key2, _ := again.JSON(t)["api_key"].(string)
	for _, k := range []string{key, key2} {
		if me := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}).JSON(t); me["username"] != reviewer {
			t.Fatalf("key after second sign-in: %v", me)
		}
	}
}

// TestReviewerSignInRefusesPasswordAccount: a password account under the
// operator's configured REVIEW_ACCOUNT_EMAIL -- admin-created here, so its
// users.username really is that address (a self sign-up's username is now
// a synthetic value and could never be found by that address in the first
// place: see TestPasswordSelfSignupNeverHijacksEmailSignIn) -- never
// proved it controls that address, so the reviewer must not be handed it
// (and the account's own password must not open the reviewer's
// connected-app access) even though the email and reviewer password both
// match.
func TestReviewerSignInRefusesPasswordAccount(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	a.Users.SetPasswordSignIn(true, true)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)
	if !a.Conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable")
	}

	if _, _, err := accountapp.New(a.Database).CreatePasswordAccount(context.Background(), reviewer, "", true, false, a.Users.AssignPasswordHandle); err != nil {
		t.Fatalf("admin-created password account under the reviewer address: %v", err)
	}

	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusForbidden || strings.Contains(string(r.Body), "api_key") {
		t.Fatalf("reviewer sign-in reused a password account: %d %s", r.Status, r.Body)
	}
}

func TestReviewerSignInRateLimit(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	reviewer := "reviewer-rl-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)
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
	a := servertest.NewConnectorApp(t)
	addr := "reviewer-admin-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountsrepo.CreateUser(context.Background(), a.Database, addr, key, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)
	a.Conn.EnableReviewerSignIn(addr, encoded)
	if r := a.ReviewerSignIn(t, addr, reviewerTestPassword, nil); r.Status != http.StatusForbidden || strings.Contains(string(r.Body), key) {
		t.Fatalf("admin reviewer account: %d %s", r.Status, r.Body)
	}
}

// A suspended reviewer account gets no new key.
func TestReviewerSignInSuspended(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	reviewer := "reviewer-susp-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)
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
