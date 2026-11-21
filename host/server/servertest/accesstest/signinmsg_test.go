package accesstest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/httpx"
)

func TestSignInHTTPMessageAnd401(t *testing.T) {
	a, _ := NewAccountApp(t)
	addr := Uniq("signin-http-") + "@example.com"
	r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": addr}, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("request: %d %s", r.Status, r.Body)
	}
	if got := r.JSON(t); got["message"] != accesshttp.SignInCodeSentMessage(addr) || got["email"] != addr {
		t.Fatalf("202 body: %s", r.Body)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": "nope"}, nil)
	if r.Status != http.StatusBadRequest || r.Code() != "invalid_email" {
		t.Fatalf("invalid email: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": addr, "code": "000000"}, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	var body httpx.ErrorResponse
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "invalid_code" || body.Reason != accesshttp.SignInReasonWrong || body.TriesLeft == nil || *body.TriesLeft != 2 {
		t.Fatalf("401 shape: %+v %s", body, r.Body)
	}
}
