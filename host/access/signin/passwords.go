package signin

import (
	"context"
	"database/sql"
	"errors"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func badCredentials() error {
	return accounts.LifecycleFail(401, "Wrong username or password.", "bad_credentials")
}

// changePasswordBadCredentials is ChangePassword's "wrong current password":
// same bad_credentials code (so the handler's limiter-refund rule still
// matches it) but 403, not 401 -- the dashboard and Hack account screens
// treat any 401 on a signed-in request as an expired key and sign the
// browser out, which a mistyped current password must not do.
func changePasswordBadCredentials() error {
	return accounts.LifecycleFail(403, "That is not your current password.", "bad_credentials")
}

func PasswordSignIn(ctx context.Context, db *sql.DB, login, password, newPassword string) (model.User, error) {
	user, hash, _, err := repo.PasswordAccount(ctx, db, accounts.NormalizeLogin(login))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.User{}, err
	}
	missing := err != nil || hash == ""
	if missing {
		hash = accounts.DummyPasswordHash
	}
	// Bound input before hashing, including invalid attempts.
	bounded := password
	if len(bounded) > 1024 {
		bounded = bounded[:1024]
	}
	matches := accounts.VerifyPassword(bounded, hash)
	if missing || !matches || accounts.ValidPassword(password) != nil {
		return model.User{}, badCredentials()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()
	lockedHash, must, err := repo.LockPassword(ctx, tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && lockedHash != hash {
		return model.User{}, badCredentials()
	}
	if err != nil {
		return model.User{}, err
	}
	if must {
		if newPassword == "" {
			return model.User{}, accounts.LifecycleFail(403, "Choose a new password to finish signing in.", "password_change_required")
		}
		if err := accounts.ValidPassword(newPassword); err != nil {
			return model.User{}, err
		}
		if password == newPassword {
			return model.User{}, accounts.LifecycleFail(400, "Choose a different password.", "invalid_password")
		}
		next, err := accounts.HashPassword(newPassword)
		if err != nil {
			return model.User{}, err
		}
		// This account was just found by repo.PasswordAccount, so its
		// account_passwords row already exists: login is never used here
		// (SetPassword only applies it to a fresh row).
		if err := repo.SetPassword(ctx, tx, user.ID, next, "", false); err != nil {
			return model.User{}, err
		}
	}
	return user, tx.Commit()
}

// ChangePassword lets a signed-in password account replace its own password:
// current must verify, next must be a different valid password. Same
// verify-then-lock-and-compare-hash pattern as PasswordSignIn, so a password
// changed between the two reads is rejected rather than silently overwritten.
// It never touches keys or sessions; every other sign-in stays valid.
func ChangePassword(ctx context.Context, db *sql.DB, userID, current, next string) error {
	hash, err := repo.PasswordHashByID(ctx, db, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return accounts.LifecycleFail(404, "not found", "")
	}
	if err != nil {
		return err
	}
	bounded := current
	if len(bounded) > 1024 {
		bounded = bounded[:1024]
	}
	matches := accounts.VerifyPassword(bounded, hash)
	if !matches || accounts.ValidPassword(current) != nil {
		return changePasswordBadCredentials()
	}
	if err := accounts.ValidPassword(next); err != nil {
		return err
	}
	if current == next {
		return accounts.LifecycleFail(400, "Choose a different password.", "invalid_password")
	}
	nextHash, err := accounts.HashPassword(next)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockedHash, _, err := repo.LockPassword(ctx, tx, userID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && lockedHash != hash {
		return changePasswordBadCredentials()
	}
	if err != nil {
		return err
	}
	// hash was loaded by PasswordHashByID above (404 if it didn't exist),
	// so this account_passwords row already exists: login is never used.
	if err := repo.SetPassword(ctx, tx, userID, nextHash, "", false); err != nil {
		return err
	}
	return tx.Commit()
}

func ResetPassword(ctx context.Context, db *sql.DB, id, password string) (model.User, string, error) {
	password, err := accounts.TemporaryPassword(password)
	if err != nil {
		return model.User{}, "", err
	}
	hash, err := accounts.HashPassword(password)
	if err != nil {
		return model.User{}, "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, "", err
	}
	defer tx.Rollback()
	user, err := repo.LockReissueAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return user, "", accounts.LifecycleFail(404, "not found", "")
	}
	if err != nil {
		return user, "", err
	}
	if accounts.IsOperator(user.IsAdmin, user.Username) {
		return user, "", accounts.LifecycleFail(400, "cannot set an admin password", "")
	}
	// Adding a password row here, including to an existing email-code or
	// OAuth account by id, is the signed-in admin's own deliberate act on an
	// account they already picked -- not an unauthenticated sign-in
	// resolving to one it never proved it owned, so it is not the
	// pre-account-hijacking case above and needs no refusal. login (used
	// only if this account never had a password row before) is the
	// account's own existing username: by the time an admin can name this
	// account by id, it already has whatever address or name it signs in
	// with, and the admin vouches for it the same as a fresh admin-created
	// account's username == login.
	if err := repo.SetPassword(ctx, tx, id, hash, accounts.NormalizeLogin(user.Username), true); err != nil {
		return user, "", err
	}
	return user, password, tx.Commit()
}
