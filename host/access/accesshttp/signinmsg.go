package accesshttp

import (
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

const (
	SignInReasonWrong   = "code_wrong"
	signInReasonUsedUp  = "code_used_up"
	signInReasonExpired = "code_expired"
	signInReasonLink    = "link_other_browser"
)

func signInCodeTTLMinutes(authTTL time.Duration) int {
	min := int(authTTL / time.Minute)
	if min < 1 {
		min = 1
	}
	return min
}

func signInCodeWrong(triesLeft int) httpx.ErrorResponse {
	msg := web.SignInMessages["code_wrong"]
	if triesLeft == 1 {
		msg = "That code is not right. 1 try left."
	} else {
		msg = strings.ReplaceAll(msg, "{n}", strconv.Itoa(triesLeft))
	}
	n := triesLeft
	return httpx.ErrorResponse{Error: msg, Reason: SignInReasonWrong, TriesLeft: &n}
}

func signInCodeUsedUp() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.SignInMessages["code_used_up"], Reason: signInReasonUsedUp}
}

func signInCodeExpired(authTTL time.Duration) httpx.ErrorResponse {
	msg := strings.ReplaceAll(web.SignInMessages["code_expired"], "{min}", strconv.Itoa(signInCodeTTLMinutes(authTTL)))
	return httpx.ErrorResponse{Error: msg, Reason: signInReasonExpired}
}

func signInLinkOtherBrowser() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.ErrLinkOtherBrowser, Reason: signInReasonLink}
}

func signInRateLimited() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.SignInMessages["rate_limited"], Code: "rate_limited"}
}

func signInInvalidEmail() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.SignInMessages["invalid_email"], Code: "invalid_email"}
}

func signInSendFailed() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.SignInMessages["send_failed"], Code: "send_failed"}
}

func signInEmailUnavailable() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: web.SignInMessages["email_unavailable"], Code: "email_unavailable"}
}

func SignInCodeSentMessage(email string) string {
	return "We sent a 6-digit code to " + email + "."
}

func signInUnauthorized(body httpx.ErrorResponse) httpx.ErrorResponse {
	body.Code = "invalid_code"
	return body
}
