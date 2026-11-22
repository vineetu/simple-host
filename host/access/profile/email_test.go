package profile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// TestEmailChangeRefusedWhenEmailSignInOff: with EMAIL_SIGNIN=off no
// emailed code is sent anywhere, so changing the sign-in email (which mails
// codes to both addresses) is refused before any lookup.
func TestEmailChangeRefusedWhenEmailSignInOff(t *testing.T) {
	withEmailSignIn(t, false)
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/me/email", nil)
	if !h.emailChangeRefused(rec, req, &model.User{Username: "person@example.com"}) {
		t.Fatal("expected the email change to be refused")
	}
	var body httpx.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got %d %q, want 403 email_signin_off", rec.Code, body.Code)
	}
}

// withEmailSignIn flips the process-wide EMAIL_SIGNIN switch for one test.
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := web.EmailSignInEnabled
	web.SetEmailSignIn(on)
	t.Cleanup(func() { web.SetEmailSignIn(previous) })
}
