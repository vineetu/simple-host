package access

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/access/repo"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
)

// ViewerAllowed reports whether account userID may open a named-viewers
// site: its owner (or a member of the team that holds it, or an editor:
// ActsAsOwner), or a named viewer by the address the account signs in with
// (VisitorEmail, the address other people's sites see). A suspended account
// never is. email is that address ("" for the owner).
func ViewerAllowed(ctx context.Context, db *sql.DB, row model.SitePasscodeRow, userID string) (ok bool, email string, err error) {
	if susp, err := New(db).UserSuspended(ctx, userID); err != nil || susp {
		return false, "", err
	}
	if owner, err := ActsAsOwner(ctx, db, userID, row.UserID, row.Name); err != nil || owner {
		return owner, "", err
	}
	email, err = VisitorEmail(ctx, db, userID)
	if err != nil {
		return false, "", err
	}
	norm, valid := authrules.NormalEmail(email)
	if !valid {
		return false, email, nil
	}
	listed, err := repo.IsSiteViewer(ctx, db, row.SiteID, norm)
	return listed, email, err
}

// VisitorEmail is the address an account presents to other people's sites
// (who may save, blocks, one per person, the _submitted_by stamp, GET /me):
// its current sign-in email, which it proved by code when it signed up or
// moved there. A linked Google address is only used for an account whose
// name is not an address (an organiser-made event account), because an
// account can move off an address it no longer owns while the identity
// linked under it stays.
//
// A self-signup password account's username is never treated as a verified
// address here, even one shaped like an email: the person who typed it never
// proved they control it, which is exactly how a password signup could
// squat someone else's email ahead of them (see the password-accounts
// pre-account-hijacking fix). An admin-created password account keeps the
// old behaviour -- the admin vouched for that address. If a self-signup
// account separately proved an address through Google/OIDC, that linked
// address is used instead; with neither, it has no verified email at all.
func VisitorEmail(ctx context.Context, database *sql.DB, userID string) (string, error) {
	u, err := New(database).GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	looksLikeEmail := strings.Contains(u.Username, "@")
	selfSignupPassword := false
	if looksLikeEmail {
		selfSignupPassword, err = New(database).SelfSignupPasswordAccount(ctx, userID)
		if err != nil {
			return "", err
		}
	}
	if looksLikeEmail && !selfSignupPassword {
		return u.Username, nil
	}
	identity, err := New(database).GetLatestOAuthIdentity(ctx, userID)
	if err == nil && identity.Email.Valid && identity.Email.String != "" {
		return identity.Email.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if looksLikeEmail {
		// A self-signup password account's email-shaped username never
		// proved it controls that address (pre-account hijacking) and there
		// is no linked OAuth identity either: no verified email to fall
		// back to, unlike an ordinary non-email username below.
		return "", nil
	}
	return u.Username, nil
}
