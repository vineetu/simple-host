package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const reviewerTestPassword = "correct horse battery staple"

func TestReviewerPasswordHash(t *testing.T) {
	if _, err := HashReviewerPassword("too-short"); err == nil {
		t.Fatal("a short password was hashed")
	}
	encoded, err := HashReviewerPassword(reviewerTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "pbkdf2-sha256$600000$") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
	h, err := parseReviewerHash(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !h.matches(reviewerTestPassword) {
		t.Fatal("the right password did not match")
	}
	for _, wrong := range []string{"", "correct horse battery stapl", reviewerTestPassword + " ", strings.ToUpper(reviewerTestPassword)} {
		if h.matches(wrong) {
			t.Fatalf("%q matched", wrong)
		}
	}
	// Two hashes of one password differ (random salt).
	again, _ := HashReviewerPassword(reviewerTestPassword)
	if again == encoded {
		t.Fatal("salt is not random")
	}
	for _, bad := range []string{
		"", "bcrypt$10$x$y", "pbkdf2-sha256$1000$" + strings.Split(encoded, "$")[2] + "$" + strings.Split(encoded, "$")[3],
		"pbkdf2-sha256$600000$!!$" + strings.Split(encoded, "$")[3], "pbkdf2-sha256$600000$" + strings.Split(encoded, "$")[2] + "$short",
		encoded + "$extra",
	} {
		if _, err := parseReviewerHash(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestReviewerSignInConfig(t *testing.T) {
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	if rv, err := newReviewerSignIn("", ""); rv != nil || err != nil {
		t.Fatal("unset should be off without an error")
	}
	for _, c := range [][2]string{{"rev@example.com", ""}, {"", encoded}, {"not-an-email", encoded}, {"rev@example.com", "garbage"}} {
		if rv, err := newReviewerSignIn(c[0], c[1]); rv != nil || err == nil {
			t.Errorf("%q/%q: enabled or no error", c[0], c[1])
		}
	}
	rv, err := newReviewerSignIn("  Rev@Example.com ", encoded)
	if err != nil || rv == nil || rv.email != "rev@example.com" {
		t.Fatalf("valid config refused: %v", err)
	}
	h := &ConnectorHandler{}
	if h.EnableReviewerSignIn("rev@example.com", "garbage") || h.reviewer != nil {
		t.Fatal("a malformed hash half-enabled reviewer sign-in")
	}
}

func TestOpenAIAppsChallenge(t *testing.T) {
	for _, c := range []struct {
		token, want string
		status      int
	}{{"", "", 404}, {"  tok_abc123  ", "tok_abc123", 200}} {
		mux := http.NewServeMux()
		RegisterOpenAIAppsChallenge(mux, c.token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/openai-apps-challenge", nil))
		if rec.Code != c.status {
			t.Fatalf("token %q: status %d", c.token, rec.Code)
		}
		if c.status == 200 {
			if rec.Body.String() != c.want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("body %q type %q", rec.Body.String(), rec.Header().Get("Content-Type"))
			}
		}
	}
}

func (a *connectorApp) reviewerSignIn(t *testing.T, email, password string, headers map[string]string) resp {
	t.Helper()
	h := map[string]string{"Content-Type": "application/json", "Origin": a.srv.URL}
	for k, v := range headers {
		h[k] = v
	}
	return a.do(t, http.MethodPost, "/oauth/reviewer-signin", jsonBody(map[string]string{"email": email, "password": password}), h)
}

func (a *connectorApp) authorizeData(t *testing.T, clientID string) map[string]any {
	t.Helper()
	_, challenge := newVerifier()
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+authorizeQuery(clientID, testRedirect, challenge).Encode(), nil, nil)
	m := connectDataRe.FindSubmatch(page.body)
	if page.status != http.StatusOK || m == nil {
		t.Fatalf("authorize page: %d", page.status)
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	return data
}

func TestReviewerSignInEndToEnd(t *testing.T) {
	a := newConnectorApp(t)
	clientID := a.registerClient(t, testRedirect)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := HashReviewerPassword(reviewerTestPassword)

	// Off by default: no endpoint, no option on the page.
	if r := a.reviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.status != http.StatusNotFound {
		t.Fatalf("disabled reviewer sign-in answered %d", r.status)
	}
	if _, shown := a.authorizeData(t, clientID)["reviewer_signin"]; shown {
		t.Fatal("consent page offers reviewer sign-in while it is off")
	}

	if !a.conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable")
	}
	if a.authorizeData(t, clientID)["reviewer_signin"] != true {
		t.Fatal("consent page does not offer reviewer sign-in")
	}

	// Another person's email with the reviewer password opens nothing, and
	// the reviewer's email with any other password opens nothing.
	other := a.newPerson(t, "someone")
	for _, c := range [][2]string{{reviewer, "wrong password, long enough"}, {other.email, reviewerTestPassword}, {"", reviewerTestPassword}, {reviewer, ""}} {
		if r := a.reviewerSignIn(t, c[0], c[1], nil); r.status != http.StatusUnauthorized || strings.Contains(string(r.body), "api_key") {
			t.Fatalf("%q/%q: %d %s", c[0], c[1], r.status, r.body)
		}
	}
	// Cross-site posts are refused before any password check.
	if r := a.reviewerSignIn(t, reviewer, reviewerTestPassword, map[string]string{"Origin": "https://evil.example"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", r.status)
	}

	// The right pair signs in as an ordinary account, created on first use.
	r := a.reviewerSignIn(t, "  "+strings.ToUpper(reviewer)+" ", reviewerTestPassword, nil)
	if r.status != http.StatusOK {
		t.Fatalf("reviewer sign-in: %d %s", r.status, r.body)
	}
	key := r.json(t)["api_key"].(string)
	me := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": key}).json(t)
	if me["username"] != reviewer || me["is_admin"] != false || me["handle"] == "" {
		t.Fatalf("reviewer account: %v", me)
	}

	// And it connects an app end to end, and publishes through it.
	access := a.connect(t, person{email: reviewer, key: key}, clientID, testRedirect)["access_token"].(string)
	text, s, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{
		"site": "reviewer-demo", "files": map[string]any{"index.html": "<h1>demo</h1>"},
	}}))
	if isErr || !strings.Contains(s["url"].(string), "/reviewer-demo/") {
		t.Fatalf("create_site as the reviewer: %s", text)
	}

	// A second sign-in returns the same account, not a new one. Keys are
	// stored hashed, so it hands out a new key and the first keeps working.
	again := a.reviewerSignIn(t, reviewer, reviewerTestPassword, nil)
	if again.status != http.StatusOK {
		t.Fatalf("second sign-in: %d", again.status)
	}
	key2, _ := again.json(t)["api_key"].(string)
	for _, k := range []string{key, key2} {
		if me := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}).json(t); me["username"] != reviewer {
			t.Fatalf("key after second sign-in: %v", me)
		}
	}
}

func TestReviewerSignInRateLimit(t *testing.T) {
	a := newConnectorApp(t)
	reviewer := "reviewer-rl-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	a.conn.EnableReviewerSignIn(reviewer, encoded)
	statuses := []int{}
	for i := 0; i < 12; i++ {
		statuses = append(statuses, a.reviewerSignIn(t, reviewer, "wrong password, long enough", nil).status)
	}
	if statuses[0] != 401 || statuses[9] != 401 || statuses[10] != 429 || statuses[11] != 429 {
		t.Fatalf("statuses %v: want ten 401s then 429", statuses)
	}
	// Once limited, even the right password waits: the limit is on attempts.
	if r := a.reviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.status != http.StatusTooManyRequests {
		t.Fatalf("right password while limited: %d", r.status)
	}
}

func TestReviewerAccountMustNotBeAdmin(t *testing.T) {
	a := newConnectorApp(t)
	addr := "reviewer-admin-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := db.CreateUser(context.Background(), a.database, addr, key, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	a.conn.EnableReviewerSignIn(addr, encoded)
	if r := a.reviewerSignIn(t, addr, reviewerTestPassword, nil); r.status != http.StatusForbidden || strings.Contains(string(r.body), key) {
		t.Fatalf("admin reviewer account: %d %s", r.status, r.body)
	}
}
