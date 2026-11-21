package accesstest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestSignInHTTPMessageAnd401(t *testing.T) {
	mb := &servertest.Mailbox{}
	a := servertest.NewPrivateAppMailer(t, mb)
	addr := "signin-http-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	t.Cleanup(func() {
		_, _ = a.Database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, addr)
		_, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, addr)
	})
	r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": addr}, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("request: %d %s", r.Status, r.Body)
	}
	got := r.JSON(t)
	if got["message"] != accesshttp.SignInCodeSentMessage(addr) || got["email"] != addr {
		t.Fatalf("202 body: %s", r.Body)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": "nope"}, nil)
	if r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_email" {
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
	if body.Error != "That code is not right. 2 tries left." {
		t.Fatalf("401 copy: %q", body.Error)
	}
}
