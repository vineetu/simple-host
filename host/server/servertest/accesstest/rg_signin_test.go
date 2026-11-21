package accesstest

import (
	"net/http"
	"testing"
)

// An emailed code works once and only while it lives: a code whose stored
// token has expired is refused even when typed correctly, and a code that
// already signed someone in cannot be replayed.
func TestSignInCodeExpiredAndReplayed(t *testing.T) {
	a, mb := NewAccountApp(t)
	request := func(addr string) string {
		t.Helper()
		if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": addr}, nil); r.Status != http.StatusAccepted {
			t.Fatalf("request: %d %s", r.Status, r.Body)
		}
		return mb.Code(addr)
	}
	verify := func(addr, code string) Resp {
		return a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": addr, "code": code}, nil)
	}

	expired := Uniq("signin-expired-") + "@example.com"
	code := request(expired)
	if _, err := a.Database.Exec(`UPDATE auth_tokens SET expires_at = now() - interval '1 minute' WHERE email = $1`, expired); err != nil {
		t.Fatal(err)
	}
	if r := verify(expired, code); r.Status != http.StatusUnauthorized || r.Code() != "invalid_code" || r.JSON(t)["reason"] != "code_expired" {
		t.Fatalf("expired code: %d %s", r.Status, r.Body)
	}

	used := Uniq("signin-replay-") + "@example.com"
	code = request(used)
	r := verify(used, code)
	if r.Status != http.StatusOK || r.JSON(t)["api_key"] == nil {
		t.Fatalf("first use: %d %s", r.Status, r.Body)
	}
	if r := verify(used, code); r.Status != http.StatusUnauthorized || r.Code() != "invalid_code" || r.JSON(t)["api_key"] != nil {
		t.Fatalf("replayed code: %d %s", r.Status, r.Body)
	}
}
