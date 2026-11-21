package accesshttp

import (
	"errors"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// signInCodeSender is the mail capability used by the account transport constructor.
// Delivery workflows are owned by app/accounts.
type signInCodeSender interface {
	SendSignInCode(address, code, link string) error
}

// codeFailureResponse words an emailed-code refusal for this transport;
// authTTL is the code lifetime an expired one names.
func codeFailureResponse(f accounts.CodeFailure, authTTL time.Duration) httpx.ErrorResponse {
	switch f.Refusal {
	case accounts.CodeExpired:
		return signInCodeExpired(authTTL)
	case accounts.CodeWrong:
		return signInCodeWrong(*f.TriesLeft)
	case accounts.CodeUsedUp:
		return signInCodeUsedUp()
	case accounts.CodeLinkOtherBrowser:
		return signInLinkOtherBrowser()
	case accounts.CodeRateLimited:
		return signInRateLimited()
	case accounts.CodeInvalidEmail:
		return signInInvalidEmail()
	case accounts.CodeSendFailed:
		return signInSendFailed()
	case accounts.CodeSignInOff:
		return web.ErrEmailSignInOff
	case accounts.CodeBlocked:
		return signupBlockedError
	}
	return httpx.ErrorResponse{Error: f.Error, Code: f.Code, Reason: f.Reason, Address: f.Address, SuggestedHandle: f.SuggestedHandle, TriesLeft: f.TriesLeft}
}

func writeLifecycleFailure(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var failure *accounts.LifecycleError
	if errors.As(err, &failure) {
		httpx.WriteJSON(w, failure.Status, httpx.ErrorResponse{Error: web.SupportText(failure.Message), Code: failure.Code})
	} else {
		httpx.WriteInternal(w)
	}
	return true
}
