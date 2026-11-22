package profile

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/model"
)

// EmailChangeError describes the existing refusal of an email change.
type EmailChangeError struct {
	Status        int
	Code, Message string
}

func (e *EmailChangeError) Error() string { return e.Message }

func emailChangeFailure(status int, code, message string) error {
	return &EmailChangeError{Status: status, Code: code, Message: message}
}

type EmailChangeResult struct{ OldEmail, NewEmail, UndoToken string }

// VerifyEmailChange holds the account row before the pending change, and keeps
// wrong-attempt accounting and address replacement in their original transactions.
func VerifyEmailChange(ctx context.Context, db *sql.DB, user *model.User, code, oldCode, reviewer string, allow func(string) bool, undoTTL time.Duration) (EmailChangeResult, error) {
	zero := EmailChangeResult{}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	oldEmail, err := repo.LockSignInEmail(ctx, tx, user.ID)
	if err != nil {
		return zero, err
	}
	change, err := repo.GetEmailChangeForUpdate(ctx, tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, emailChangeFailure(400, "no_pending_change", "no email change is waiting, or its code expired; ask for a new code")
	}
	if err != nil {
		return zero, err
	}
	if change.Attempts >= 3 {
		return zero, emailChangeFailure(401, "invalid_code", "too many attempts, request a new code")
	}
	if !allow(change.NewEmail) {
		return zero, emailChangeFailure(429, "", "rate limit exceeded, slow down")
	}
	newOK := subtle.ConstantTimeCompare([]byte(hashChangeCode(code)), []byte(change.CodeHash)) == 1
	oldOK := change.OldCodeHash != "" && subtle.ConstantTimeCompare([]byte(hashChangeCode(oldCode)), []byte(change.OldCodeHash)) == 1
	if !newOK || !oldOK {
		if err := repo.CountEmailChangeAttempt(ctx, tx, user.ID); err == nil {
			_ = tx.Commit()
		}
		return zero, emailChangeFailure(401, "invalid_code", "invalid or expired code")
	}
	if reviewer != "" && strings.EqualFold(change.NewEmail, reviewer) {
		return zero, emailChangeFailure(400, "reserved_email", "that address cannot be used to sign in")
	}
	taken, err := repo.EmailInUseByOther(ctx, tx, user.ID, change.NewEmail)
	if err != nil {
		return zero, err
	}
	if taken {
		return zero, emailChangeFailure(409, "email_taken", "another Simple Host account already signs in with that address")
	}
	undo, err := accounts.SecureHex(24)
	if err != nil {
		return zero, err
	}
	err = repo.ApplyEmailChange(ctx, tx, user.ID, oldEmail, change.NewEmail, user.KeyHash, hashChangeCode(undo), undoTTL)
	if err == nil {
		err = tx.Commit()
	}
	if repo.IsUniqueViolation(err) {
		return zero, emailChangeFailure(409, "email_taken", "another Simple Host account already signs in with that address")
	}
	if err != nil {
		log.Printf("email change user_id=%s: apply: %v", user.ID, err)
		return zero, err
	}
	return EmailChangeResult{OldEmail: oldEmail, NewEmail: change.NewEmail, UndoToken: undo}, nil
}

func hashChangeCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func PutEmailChange(ctx context.Context, db *sql.DB, userID, address, code, oldCode string, expires time.Time) error {
	return repo.PutEmailChange(ctx, db, userID, address, hashChangeCode(code), hashChangeCode(oldCode), expires)
}

func EmailChangeTarget(ctx context.Context, db *sql.DB, userID string) (model.SignInAlertTarget, error) {
	return repo.GetSignInAlertTarget(ctx, db, userID)
}

func GetEmailChangeUndo(ctx context.Context, db *sql.DB, hash string) (model.EmailChangeUndo, error) {
	return repo.GetEmailChangeUndo(ctx, db, hash)
}

func UndoEmailChange(ctx context.Context, db *sql.DB, hash string) (model.EmailChangeUndo, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.EmailChangeUndo{}, err
	}
	defer tx.Rollback()
	result, err := repo.UndoEmailChangeInTx(ctx, tx, hash)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// UnlinkSignInIdentity: that Google or GitHub account no longer signs in to
// this one; a 404 *LifecycleError when the account has no such sign-in.
func UnlinkSignInIdentity(ctx context.Context, db *sql.DB, userID, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.UnlinkSignInIdentityInTx(ctx, tx, userID, id); errors.Is(err, sql.ErrNoRows) {
		return &accounts.LifecycleError{Status: http.StatusNotFound, Message: "no such sign-in on this account"}
	} else if err != nil {
		return err
	}
	return tx.Commit()
}

// RequestEmailChange records both inbox challenges before sending either message.
func RequestEmailChange(ctx context.Context, db *sql.DB, userID, address, current string, mailer accounts.CodeSender, ttl time.Duration, supportText func(string) string) error {
	code, err := accounts.NumericCode(6)
	if err != nil {
		return err
	}
	oldCode, err := accounts.NumericCode(6)
	if err != nil {
		return err
	}
	if err := PutEmailChange(ctx, db, userID, address, code, oldCode, time.Now().Add(ttl)); err != nil {
		log.Printf("email change user_id=%s: store: %v", userID, err)
		return err
	}
	if err := SendEmailChangeCode(mailer, address, code, ttl); err != nil {
		log.Printf("email change: send code to %s: %v", accounts.RedactedEmail(address), err)
		return emailChangeFailure(500, "", "could not send verification email")
	}
	if err := SendEmailChangeCurrentCode(mailer, current, address, oldCode, ttl, supportText); err != nil {
		log.Printf("email change: send code to current %s: %v", accounts.RedactedEmail(current), err)
		return emailChangeFailure(500, "", "could not send verification email")
	}
	return nil
}

func maskEmail(address string) string {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" {
		return address
	}
	return local[:1] + "***@" + domain
}

func SendEmailChangeCode(mailer accounts.CodeSender, address, code string, ttl time.Duration) error {
	notice, ok := mailer.(accounts.NoticeSender)
	if !ok {
		return mailer.SendSignInCode(address, code, "")
	}
	return notice.SendNotice(address, "Simple Host confirmation code: "+code, `Someone asked to make this address the sign-in email of a Simple Host account.

Your confirmation code:

    `+code+`

It expires in `+config.Span(ttl)+`. If you didn't ask for this, ignore this email: nothing changes.

Simple Host
`)
}

func SendEmailChangeCurrentCode(mailer accounts.CodeSender, current, newAddress, code string, ttl time.Duration, supportText func(string) string) error {
	notice, ok := mailer.(accounts.NoticeSender)
	if !ok {
		return mailer.SendSignInCode(current, code, "")
	}
	return notice.SendNotice(current, "Simple Host: confirm changing your sign-in email ("+code+")", supportText(`Someone signed in to your Simple Host account asked to change its sign-in email to `+maskEmail(newAddress)+`.

If that was you, enter this code together with the one sent to the new address:

    `+code+`

It expires in `+config.Span(ttl)+`. If it wasn't you, do not share this code: without it nothing changes. Someone has one of your keys, so sign in and remove the keys you don't recognise, or write to support@simple-host.app.

Simple Host
`))
}

func EmailChangedNotice(newEmail, undoLink string, undoTTL time.Duration) string {
	text := "Your Simple Host sign-in email was changed to " + maskEmail(newEmail) + `. From now on you sign in with that address.

If this wasn't you, undo it (opens a page with an Undo button; works for ` + config.Span(undoTTL) + `):
` + undoLink + `

Undoing puts the account back on this address, signs out every key and every sign-in on sites, and removes Google or GitHub sign-ins and connected apps added since the change. Questions: support@simple-host.app.

Simple Host
`
	return text
}

// NotifyEmailChanged schedules the old-inbox notice only when delivery is supported.
func NotifyEmailChanged(sender accounts.CodeSender, oldEmail, newEmail, undoLink string, undoTTL time.Duration, supportText func(string) string, run func(func())) {
	mailer, ok := sender.(accounts.NoticeSender)
	if !ok || !strings.Contains(oldEmail, "@") {
		return
	}
	text := EmailChangedNotice(newEmail, undoLink, undoTTL)
	run(func() {
		if err := mailer.SendNotice(oldEmail, "Your Simple Host sign-in email was changed", supportText(text)); err != nil {
			log.Printf("email change: notice to old address: %v", err)
		}
	})
}

// EmailChangeRefusal is why the account cannot change its sign-in email (a
// 403 *LifecycleError), nil when it can: the reviewer's account
// (reviewerEmail) and the PREVIEW_ACCOUNTS have theirs set by the operator,
// the admin's cannot change here, an account without an email address has
// none to confirm the change from, and an account that holds an event keeps
// its address.
func EmailChangeRefusal(ctx context.Context, db *sql.DB, user *model.User, reviewerEmail string, previewAccounts map[string]bool) error {
	refuse := func(code, msg string) error {
		return &accounts.LifecycleError{Status: http.StatusForbidden, Message: msg, Code: code}
	}
	switch {
	case reviewerEmail != "" && strings.EqualFold(user.Username, reviewerEmail):
		return refuse("reviewer_account", "this account's email is set by the operator")
	case accounts.IsOperator(user.IsAdmin, user.Username):
		return refuse("admin_account", "the admin account's sign-in email cannot be changed here")
	case previewAccounts[strings.ToLower(user.Username)]:
		return refuse("preview_account", "this account's email is set by the operator")
	case !strings.Contains(user.Username, "@"):
		return refuse("no_current_email", "this account does not sign in with an email address, so there is none to confirm the change from; write to support@simple-host.app")
	}
	t, err := EmailChangeTarget(ctx, db, user.ID)
	if err != nil {
		return err
	}
	if t.Event {
		return refuse("event_account", accounts.HoldingText("email"))
	}
	return nil
}
