package servertest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
)

// The plugin-directory reviewer signs in on Simple Hack's consent page with
// the configured email and password (no emailed code), as an ordinary
// account, and the connection it allows reaches the event tools.
func TestHackReviewerSignIn(t *testing.T) {
	a := newTeamSiteApp(t)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	const password = "correct horse battery staple"
	encoded, err := accesshttp.HashReviewerPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !a.conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable reviewer sign-in")
	}
	clientID := a.registerClient(t, testRedirect)
	_, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	m := connectDataRe.FindSubmatch(page.body)
	var data map[string]any
	if m == nil || json.Unmarshal(m[1], &data) != nil || data["reviewer_signin"] != true || data["hack"] != true {
		t.Fatalf("Hack consent page does not offer reviewer sign-in: %d %s", page.status, m)
	}

	signIn := a.do(t, http.MethodPost, "/oauth/reviewer-signin", jsonBody(map[string]string{"email": reviewer, "password": password}),
		map[string]string{"Content-Type": "application/json", "Origin": a.srv.URL})
	if signIn.status != http.StatusOK {
		t.Fatalf("reviewer sign-in: %d %s", signIn.status, signIn.body)
	}
	key, _ := signIn.json(t)["api_key"].(string)
	me := a.api(t, http.MethodGet, "/v1/me", nil, key).json(t)
	if me["username"] != reviewer || me["is_admin"] != false {
		t.Fatalf("reviewer account: %v", me)
	}

	access := a.connect(t, person{email: reviewer, key: key}, clientID, testRedirect)["access_token"].(string)
	text, _, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_list_events", "arguments": map[string]any{}}))
	if isErr {
		t.Fatalf("hack_list_events as the reviewer: %s", text)
	}
}
