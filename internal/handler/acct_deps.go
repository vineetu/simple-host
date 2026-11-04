package handler

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/app/accounts"
	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
	"net/http"
	"time"
)

// signInCodeSender is the mail capability used by the account transport constructor.
// Delivery workflows are owned by app/accounts.
type signInCodeSender interface {
	SendSignInCode(address, code, link string) error
}

// accountQuerier lets legacy entry points pass an existing transaction to the
// application workflow without importing a database package into the transport.
type accountQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func codeFailureResponse(f accounts.CodeFailure) errorResponse {
	return errorResponse{Error: f.Error, Code: f.Code, Reason: f.Reason, Address: f.Address, SuggestedHandle: f.SuggestedHandle, TriesLeft: f.TriesLeft}
}
func toCodeFailure(f errorResponse) accounts.CodeFailure {
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
		Off:              toCodeFailure(errEmailSignInOff), Blocked: toCodeFailure(signupBlockedError),
	}
}
func codeHandleActions(database *sql.DB) accounts.HandleActions {
	return accounts.HandleActions{
		Judge: func(ctx context.Context, userID, handle string) (model.HandleVerdict, error) {
			return JudgeHandle(ctx, database, userID, handle)
		},
		Suggest: func(ctx context.Context, email string) string { return suggestHandle(ctx, database, email) },
		Address: handleAddress,
		Assign: func(ctx context.Context, userID, email string, random bool) {
			assignHandle(ctx, database, userID, email, random)
		},
	}
}

func writeLifecycleFailure(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var failure *accounts.LifecycleError
	if errors.As(err, &failure) {
		writeJSON(w, failure.Status, errorResponse{Error: supportText(failure.Message), Code: failure.Code})
	} else {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
	}
	return true
}

func writeAccessMutationError(w http.ResponseWriter, err error) {
	var marker *appsites.Error
	if errors.As(err, &marker) && marker.Code == "access_marker" {
		writeJSON(w, 500, errorResponse{Error: marker.Message})
		return
	}
	writeSiteLookupError(w, err)
}
