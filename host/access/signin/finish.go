package signin

import (
	"context"
	"database/sql"
	"log"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/model"
)

// SuspendedError: the operator suspended the account; Reason is the one they
// gave. It is a model.ErrAccountSuspended.
type SuspendedError struct{ Reason string }

func (e *SuspendedError) Error() string { return model.ErrAccountSuspended.Error() }
func (e *SuspendedError) Unwrap() error { return model.ErrAccountSuspended }

// IssueSignInKey ends every sign-in: plain becomes a new key named name for
// the account (keys are stored only as hashes, so each sign-in hands out its
// own; earlier ones keep working until the person rotates). A suspended
// account gets none and, having proved its identity, is told why
// (*SuspendedError); one suspended between that read and the insert is a
// bare model.ErrAccountSuspended. With withLogin it also returns a password
// account's own login (account_passwords.login), never its possibly
// synthetic username: a cosmetic field, so a failed lookup logs and leaves
// it "".
func IssueSignInKey(ctx context.Context, db *sql.DB, userID, plain, name string, withLogin bool) (string, error) {
	fresh, err := accounts.New(db).GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if fresh.Suspended {
		return "", &SuspendedError{Reason: fresh.SuspendedReason}
	}
	if err := accounts.New(db).AddSignInKey(ctx, userID, plain, name); err != nil {
		return "", err
	}
	if !withLogin {
		return "", nil
	}
	login, has, err := accounts.New(db).PasswordLogin(ctx, userID)
	if err != nil {
		log.Printf("auth: password login lookup after verify: %v", err)
		return "", nil
	}
	if !has {
		return "", nil
	}
	return login, nil
}

// RecordNewSignup records where a new account came from: a sign-up that
// came through a visitor sign-in hop (ssoState names a live one) counts as
// "visitor_sso". A failed write only logs (RecordSignup).
func RecordNewSignup(ctx context.Context, db *sql.DB, userID string, source model.Signup, ssoState string) {
	if ssoState != "" {
		if _, err := accounts.New(db).GetSSOHop(ctx, ssoState, false); err == nil {
			source.Source = "visitor_sso"
		}
	}
	accounts.RecordSignup(ctx, db, userID, source)
}
