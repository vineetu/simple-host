package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"time"
)

// Sign-in email changes and sign-in alerts (w2-account-signin-email.sql).

// EmailChange is an account's pending sign-in email change: one code went to
// the new address and one to the current one; both are needed.
type EmailChange = model.EmailChange

// ApplyEmailChange moves the account to newEmail inside tx and:
//   - drops the pending change;
//   - retires every unused emailed code and link for the old address (one
//     redeemed later would otherwise create a new, empty account under it;
//     verifyEmailCode claims its token in a transaction, so the two
//     serialize on the token row);
//   - revokes every other API key of the account except keepKeyHash (the key
//     that made the change);
//   - ends the account's emailed idle-cleanup links (they went to the old
//     address);
//   - ends every site sign-in of the account (EndVisitorSessions): whoever
//     made the change may have signed in on the owner's own sites;
//   - records the undo link (undoHash) the old address is sent, valid for
//     EmailChangeUndoTTL.
//
// The caller has checked that no other account holds newEmail; a race still
// ends in a unique violation on users.username.
func ApplyEmailChange(ctx context.Context, tx *sql.Tx, userID, oldEmail, newEmail, keepKeyHash, undoHash string, undoTTL time.Duration) error {
	return accounts.ApplyEmailChange(ctx, tx, userID, oldEmail, newEmail, keepKeyHash, undoHash, undoTTL)
}

// EmailChangeUndo is an unused, unexpired undo link's change.
type EmailChangeUndo = model.EmailChangeUndo

// ErrUndoAddressTaken: the old address signs in to another account now.
var ErrUndoAddressTaken = model.ErrUndoAddressTaken

// UndoEmailChange puts the account back on the old address ("this wasn't
// me"), in one transaction: every API key is revoked (whoever made the change
// holds one), every site sign-in ends, Google/GitHub sign-ins and connected
// apps added since the change
// are removed, pending changes, emailed codes for the address it had and
// idle-cleanup links end, and every undo link of the account is spent.
// sql.ErrNoRows when the link is unknown, used or expired; ErrUndoAddressTaken
// when another account signs in with the old address now.
func UndoEmailChange(ctx context.Context, database *sql.DB, tokenHash string) (EmailChangeUndo, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return EmailChangeUndo{}, err
	}
	defer tx.Rollback()
	u, err := accounts.UndoEmailChangeInTx(ctx, tx, tokenHash)
	if err != nil {
		return u, err
	}
	return u, tx.Commit()
}

// SignInAlertTarget is what deciding and addressing a sign-in alert needs.
type SignInAlertTarget = model.SignInAlertTarget

// GetSignInAlertTarget loads the account's alert settings.
func GetSignInAlertTarget(ctx context.Context, q Querier, userID string) (SignInAlertTarget, error) {
	return accounts.GetSignInAlertTarget(ctx, q, userID)
}
