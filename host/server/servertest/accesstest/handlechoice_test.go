package accesstest

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// A new account chooses its address at sign-up (owner decision 2026-09-28):
// choose_handle answers 409 with a suggestion and creates nothing; a refused
// handle does not use up the code; the chosen one is the account's address.
// An existing account signs in as before.
func TestSignUpChoosesHandle(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	taken := a.NewPerson(t, "taken")
	takenHandle := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": taken.Key}).JSON(t)["handle"].(string)

	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	addr := "newbie-" + stamp + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, addr) })
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, "424242", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	verify := func(body map[string]any) servertest.Resp {
		return a.Do(t, http.MethodPost, "/v1/auth/verify", servertest.JSONBody(body), map[string]string{"Content-Type": "application/json"})
	}
	exists := func() bool {
		var n int
		_ = a.Database.QueryRow(`SELECT count(*) FROM users WHERE username = $1`, addr).Scan(&n)
		return n > 0
	}

	r := verify(map[string]any{"email": addr, "code": "424242", "choose_handle": true})
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "choose_handle" || r.JSON(t)["suggested_handle"] != "newbie-"+stamp {
		t.Fatalf("choose_handle: %d %s", r.Status, r.Body)
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
		// JudgeHandle, covered by TestHandleCheck.
		{takenHandle, http.StatusConflict, "handle_taken"},
	} {
		r := verify(map[string]any{"email": addr, "code": "424242", "handle": c.handle})
		if r.Status != c.status || r.JSON(t)["code"] != c.code {
			t.Fatalf("handle %q: %d %s", c.handle, r.Status, r.Body)
		}
		if exists() {
			t.Fatalf("handle %q created the account", c.handle)
		}
	}
	want := "chosen-" + stamp
	r = verify(map[string]any{"email": addr, "code": "424242", "handle": want})
	if r.Status != http.StatusOK || r.JSON(t)["handle"] != want || r.JSON(t)["created"] != true {
		t.Fatalf("chosen handle: %d %s", r.Status, r.Body)
	}

	// An existing account: choose_handle and handle change nothing.
	lt2, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, "434343", lt2, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	r = verify(map[string]any{"email": addr, "code": "434343", "choose_handle": true, "handle": "something-else-" + stamp})
	if r.Status != http.StatusOK || r.JSON(t)["handle"] != want {
		t.Fatalf("existing account: %d %s", r.Status, r.Body)
	}
}

// GET /v1/handles/check: available, taken, reserved, malformed; your own
// handle is yours; a cross-site browser request is refused.
func TestHandleCheck(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "checker")
	own := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": p.Key}).JSON(t)["handle"].(string)
	check := func(h string, hdr map[string]string) servertest.Resp {
		return a.Do(t, http.MethodGet, "/v1/handles/check?handle="+h, nil, hdr)
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	if r := check("free-"+stamp, nil); r.Status != 200 || r.JSON(t)["available"] != true {
		t.Fatalf("free: %d %s", r.Status, r.Body)
	}
	if r := check(own, nil); r.JSON(t)["available"] != false || r.JSON(t)["code"] != "handle_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	if r := check(own, map[string]string{"X-API-Key": p.Key}); r.JSON(t)["available"] != true {
		t.Fatalf("own: %d %s", r.Status, r.Body)
	}
	if r := check("admin", nil); r.JSON(t)["code"] != "handle_reserved" {
		t.Fatalf("reserved: %d %s", r.Status, r.Body)
	}
	if r := check("Not_OK", nil); r.JSON(t)["code"] != "invalid_handle" {
		t.Fatalf("invalid: %d %s", r.Status, r.Body)
	}
	if r := check("x", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.Status != http.StatusForbidden {
		t.Fatalf("cross-site: %d", r.Status)
	}
}
