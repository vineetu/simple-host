package signin

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// A new account chooses its address at sign-up (owner decision 2026-09-28):
// choose_handle answers 409 with a suggestion and creates nothing; a refused
// handle does not use up the code; the chosen one is the account's address.
// An existing account signs in as before.
func TestSignUpChoosesHandle(t *testing.T) {
	a := NewApp(t)
	taken := a.NewPerson(t, "taken")
	takenHandle := taken.Handle

	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	addr := "newbie-" + stamp + "@example.com"
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, "424242", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	verify := func(body map[string]any) Resp {
		return a.Do(t, http.MethodPost, "/v1/auth/verify", body, map[string]string{"Content-Type": "application/json"})
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
	// One refusal here: every verify counts against the address's sign-in
	// limit; the other refusals are the same JudgeHandle, covered by TestHandleCheck.
	r = verify(map[string]any{"email": addr, "code": "424242", "handle": takenHandle})
	if r.Status != http.StatusConflict || r.Code() != "handle_taken" || exists() {
		t.Fatalf("taken handle: %d %s", r.Status, r.Body)
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
