package accesshttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// signInCodeSender is the mail capability used by the account transport constructor.
// Delivery workflows are owned by app/accounts.
type signInCodeSender interface {
	SendSignInCode(address, code, link string) error
}

func codeFailureResponse(f accounts.CodeFailure) httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: f.Error, Code: f.Code, Reason: f.Reason, Address: f.Address, SuggestedHandle: f.SuggestedHandle, TriesLeft: f.TriesLeft}
}
func toCodeFailure(f httpx.ErrorResponse) accounts.CodeFailure {
	return accounts.CodeFailure{Error: f.Error, Code: f.Code, Reason: f.Reason, Address: f.Address, SuggestedHandle: f.SuggestedHandle, TriesLeft: f.TriesLeft}
}
func codeMessages() accounts.CodeMessages {
	return accounts.CodeMessages{
		Expired:          func(d time.Duration) accounts.CodeFailure { return toCodeFailure(signInCodeExpired(d)) },
		Wrong:            func(n int) accounts.CodeFailure { return toCodeFailure(signInCodeWrong(n)) },
		UsedUp:           func() accounts.CodeFailure { return toCodeFailure(signInCodeUsedUp()) },
		LinkOtherBrowser: func() accounts.CodeFailure { return toCodeFailure(signInLinkOtherBrowser()) },
		RateLimited:      func() accounts.CodeFailure { return toCodeFailure(signInRateLimited()) },
		InvalidEmail:     func() accounts.CodeFailure { return toCodeFailure(signInInvalidEmail()) },
		SendFailed:       func() accounts.CodeFailure { return toCodeFailure(signInSendFailed()) },
		Off:              toCodeFailure(web.ErrEmailSignInOff), Blocked: toCodeFailure(signupBlockedError),
	}
}
func codeHandleActions(database *sql.DB) accounts.HandleActions {
	return accounts.HandleActions{
		Judge: func(ctx context.Context, userID, handle string) (model.HandleVerdict, error) {
			return accounts.JudgeHandle(ctx, database, userID, handle)
		},
		Suggest: func(ctx context.Context, email string) string { return accounts.SuggestHandle(ctx, database, email) },
		Address: accounts.HandleAddress,
		Assign: func(ctx context.Context, userID, email string, random bool) {
			accounts.AssignHandle(ctx, database, userID, email, random)
		},
	}
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
