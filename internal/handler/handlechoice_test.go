package handler

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// A new account chooses its address at sign-up (owner decision 2026-09-28):
// choose_handle answers 409 with a suggestion and creates nothing; a refused
// handle does not use up the code; the chosen one is the account's address.
// An existing account signs in as before.
func TestSignUpChoosesHandle(t *testing.T) {
	a := newConnectorApp(t)
	taken := a.newPerson(t, "taken")
	takenHandle := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": taken.key}).json(t)["handle"].(string)

	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	addr := "newbie-" + stamp + "@example.com"
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, addr) })
	lt, _ := auth.GenerateAPIKey()
	if err := db.CreateAuthToken(context.Background(), a.database, addr, "424242", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	verify := func(body map[string]any) resp {
		return a.do(t, http.MethodPost, "/v1/auth/verify", jsonBody(body), map[string]string{"Content-Type": "application/json"})
	}
	exists := func() bool {
		var n int
		_ = a.database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, addr).Scan(&n)
		return n > 0
	}

	r := verify(map[string]any{"email": addr, "code": "424242", "choose_handle": true})
	if r.status != http.StatusConflict || r.json(t)["code"] != "choose_handle" || r.json(t)["suggested_handle"] != "newbie-"+stamp {
		t.Fatalf("choose_handle: %d %s", r.status, r.body)
	}
	if exists() {
		t.Fatal("choose_handle created the account")
	}
	for _, c := range []struct {
		handle string
		status int
		code   string
	}{
		// One refusal here: every verify counts against the address's
		// sign-in limit (5 at once); the other refusals are the same
		// judgeHandle, covered by TestHandleCheck.
		{takenHandle, http.StatusConflict, "handle_taken"},
	} {
		r := verify(map[string]any{"email": addr, "code": "424242", "handle": c.handle})
		if r.status != c.status || r.json(t)["code"] != c.code {
			t.Fatalf("handle %q: %d %s", c.handle, r.status, r.body)
		}
		if exists() {
			t.Fatalf("handle %q created the account", c.handle)
		}
	}
	want := "chosen-" + stamp
	r = verify(map[string]any{"email": addr, "code": "424242", "handle": want})
	if r.status != http.StatusOK || r.json(t)["handle"] != want || r.json(t)["created"] != true {
		t.Fatalf("chosen handle: %d %s", r.status, r.body)
	}

	// An existing account: choose_handle and handle change nothing.
	lt2, _ := auth.GenerateAPIKey()
	if err := db.CreateAuthToken(context.Background(), a.database, addr, "434343", lt2, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	r = verify(map[string]any{"email": addr, "code": "434343", "choose_handle": true, "handle": "something-else-" + stamp})
	if r.status != http.StatusOK || r.json(t)["handle"] != want {
		t.Fatalf("existing account: %d %s", r.status, r.body)
	}
}

// GET /v1/handles/check: available, taken, reserved, malformed; your own
// handle is yours; a cross-site browser request is refused.
func TestHandleCheck(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "checker")
	own := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": p.key}).json(t)["handle"].(string)
	check := func(h string, hdr map[string]string) resp {
		return a.do(t, http.MethodGet, "/v1/handles/check?handle="+h, nil, hdr)
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	if r := check("free-"+stamp, nil); r.status != 200 || r.json(t)["available"] != true {
		t.Fatalf("free: %d %s", r.status, r.body)
	}
	if r := check(own, nil); r.json(t)["available"] != false || r.json(t)["code"] != "handle_taken" {
		t.Fatalf("taken: %d %s", r.status, r.body)
	}
	if r := check(own, map[string]string{"X-API-Key": p.key}); r.json(t)["available"] != true {
		t.Fatalf("own: %d %s", r.status, r.body)
	}
	if r := check("admin", nil); r.json(t)["code"] != "handle_reserved" {
		t.Fatalf("reserved: %d %s", r.status, r.body)
	}
	if r := check("Not_OK", nil); r.json(t)["code"] != "invalid_handle" {
		t.Fatalf("invalid: %d %s", r.status, r.body)
	}
	if r := check("x", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-site: %d", r.status)
	}
}
