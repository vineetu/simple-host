package signin

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/web"
)

func TestPasswordAccountsDisabled(t *testing.T) {
	for _, selfSignup := range []bool{false, true} {
		t.Run(fmt.Sprintf("password-enabled-%v", selfSignup), func(t *testing.T) {
			a := NewApp(t)
			path := "/v1/auth/password"
			if selfSignup {
				a.Users.SetPasswordSignIn(true, false)
				path += "/signup"
			}
			r := a.Do(t, "POST", path, rt.JSONBody(map[string]string{"username": "signup-off", "password": "first password"}), map[string]string{"Content-Type": "application/json"})
			if r.Status != 404 {
				t.Fatalf("disabled: %d %s", r.Status, r.Body)
			}
		})
	}
}

func TestPasswordLoginLimit(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	for i := 0; i < 11; i++ {
		r := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": "missing-login", "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 10 {
			if r.Status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.Status, r.Body)
			}
		} else if r.Status != 429 || r.Header.Get("Retry-After") == "" {
			t.Fatalf("limit: %d %s", r.Status, r.Body)
		}
	}
}

func TestPasswordIPLimitAcrossUsernames(t *testing.T) {
	limits := config.DefaultLimits()
	limits.RateSigninIP = config.Rate{Burst: 3, Every: time.Hour}
	a := NewApp(t, edition.Default(limits))
	a.Users.SetPasswordSignIn(true, true)
	for i := 0; i < 4; i++ {
		r := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": fmt.Sprintf("missing-%d", i), "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 3 {
			if r.Status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.Status, r.Body)
			}
		} else if r.Status != 429 || r.Header.Get("Retry-After") == "" {
			t.Fatalf("IP limit: %d %s", r.Status, r.Body)
		}
	}
}

// TestPasswordSelfSignupNeverHijacksEmailSignIn covers the redesigned
// pre-account-hijacking fix (2026-11-05): a self sign-up's users.username is
// now a synthetic value, so it can never be found by address at all -- an
// email-code sign-in to the address the squatter typed as their login
// creates or uses a SEPARATE, ordinary email account, never resolving to or
// touching the password account.
func TestPasswordSelfSignupNeverHijacksEmailSignIn(t *testing.T) {
	withEmailSignIn(t, true)
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	ctx := context.Background()

	address := fmt.Sprintf("hijack-target-%d@example.com", time.Now().UnixNano())
	signup := a.Do(t, "POST", "/v1/auth/password/signup", rt.JSONBody(map[string]string{"username": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signup.Status != 200 {
		t.Fatalf("attacker signup: %d %s", signup.Status, signup.Body)
	}
	pwID := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", pwID) })

	// The real owner now proves control of the same address with an
	// emailed code.
	code := "913042"
	linkToken := fmt.Sprintf("hijack-link-%d", time.Now().UnixNano())
	if err := accountsrepo.CreateAuthToken(ctx, a.Database, address, code, linkToken, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM auth_tokens WHERE email=$1", address) })

	user, created, status, body := web.VerifyEmailCode(ctx, a.Database, a.Users.EmailLimiter, access.VerifyCodeRequest{Email: address, Code: code}, "dashboard", sql.NullString{}, false, 15*time.Minute)
	if status != 0 {
		t.Fatalf("email code sign-in to a separate address was refused: status=%d body=%+v", status, body)
	}
	if !created {
		t.Fatalf("email code sign-in did not create a new, separate account")
	}
	if user.ID == pwID {
		t.Fatalf("email code sign-in resolved into the password account")
	}
	if user.Username != address {
		t.Fatalf("new email account username: got %q want %q", user.Username, address)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", user.ID) })

	// The password account itself is untouched: the squatter's original
	// password still signs in, into its own separate account.
	signin := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signin.Status != 200 || signin.JSON(t)["id"] != pwID {
		t.Fatalf("password account was affected by the separate email sign-in: %d %s", signin.Status, signin.Body)
	}
}

// TestPasswordUsernameBoundaries covers validUsername's sanity limits at
// their edges: 1 rune and 254 multibyte runes are both fine (no character
// class, no format gating), but a username carrying a control character is
// refused regardless of length.
func TestPasswordUsernameBoundaries(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	post := func(username string) Resp {
		return a.Do(t, "POST", "/v1/auth/password/signup", rt.JSONBody(map[string]string{"username": username, "password": "boundary password"}), map[string]string{"Content-Type": "application/json"})
	}

	// 1 rune, from the Unicode Private Use Area so it never collides with
	// anything a real sign-up would choose.
	one := post("")
	if one.Status != 200 {
		t.Fatalf("1-rune username refused: %d %s", one.Status, one.Body)
	}
	if id, ok := one.JSON(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// 254 multibyte runes (508 UTF-8 bytes): still within the 254-rune limit.
	long := strings.Repeat("é", 254)
	exact := post(long)
	if exact.Status != 200 {
		t.Fatalf("254-multibyte-rune username refused: %d %s", exact.Status, exact.Body)
	}
	if id, ok := exact.JSON(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// An embedded control character (BEL) is refused regardless of length.
	withControl := post("abc\x07def")
	if withControl.Status != 400 || withControl.JSON(t)["code"] != "invalid_username" {
		t.Fatalf("control character accepted: %d %s", withControl.Status, withControl.Body)
	}
}

// The operator's "admin" row is protected even when its is_admin flag is
// off (accounts.IsOperator): its password cannot be set here. Needs DB_DSN.
func TestResetPasswordRefusesOperatorRow(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	var id string
	if err := a.Database.QueryRow(`UPDATE users SET is_admin = false WHERE username = 'admin' RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, id) })
	r := a.Do(t, "POST", "/v1/admin/users/"+id+"/password", rt.JSONBody(map[string]string{"password": "reset password"}), map[string]string{"Content-Type": "application/json", "X-API-Key": a.Admin})
	if r.Status != 400 {
		t.Fatalf("operator row password reset: %d %s", r.Status, r.Body)
	}
}
