package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

func NormalizeLogin(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// validLogin is a sanity limit, not format gating (owner decision,
// 2026-11-05): the person may type any email or made-up login they like --
// no character class, no email-only allowlist. It is already normalized
// (NormalizeLogin: trimmed, lower-cased) by the time it reaches here. Only
// empty, over-long or control-character input is refused.
func validLogin(login string) error {
	n := utf8.RuneCountInString(login)
	if n == 0 || n > 254 {
		return lifecycleFail(400, "Enter an email or username, up to 254 characters.", "invalid_username")
	}
	for _, r := range login {
		if unicode.IsControl(r) {
			return lifecycleFail(400, "Enter an email or username, up to 254 characters.", "invalid_username")
		}
	}
	return nil
}

// randomPasswordUsername is a self sign-up account's users.username: never
// shaped like an email and never meaningful, so nothing elsewhere in the
// codebase that treats users.username as a verified address -- visitor/saver
// email resolution, Hack voting identity, sign-in alert mail, Hack notice
// delivery, named-viewer sign-in history -- can be fooled by a self-chosen
// value that looks like someone else's address. What the person actually
// typed lives only in account_passwords.login (see CreatePasswordAccount).
func randomPasswordUsername() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pw-" + hex.EncodeToString(b), nil
}
func badCredentials() error {
	return lifecycleFail(401, "Wrong username or password.", "bad_credentials")
}

// changePasswordBadCredentials is ChangePassword's "wrong current password":
// same bad_credentials code (so the handler's limiter-refund rule still
// matches it) but 403, not 401 -- the dashboard and Hack account screens
// treat any 401 on a signed-in request as an expired key and sign the
// browser out, which a mistyped current password must not do.
func changePasswordBadCredentials() error {
	return lifecycleFail(403, "That is not your current password.", "bad_credentials")
}
func (s *Service) PasswordSignIn(ctx context.Context, login, password, newPassword string) (model.User, error) {
	user, hash, _, err := repo.PasswordAccount(ctx, s.database, NormalizeLogin(login))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.User{}, err
	}
	missing := err != nil || hash == ""
	if missing {
		hash = dummyPasswordHash
	}
	// Bound input before hashing, including invalid attempts.
	bounded := password
	if len(bounded) > 1024 {
		bounded = bounded[:1024]
	}
	matches := verifyPassword(bounded, hash)
	if missing || !matches || validPassword(password) != nil {
		return model.User{}, badCredentials()
	}
	tx, err := s.database.BeginTx(ctx, nil)
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
			return model.User{}, lifecycleFail(403, "Choose a new password to finish signing in.", "password_change_required")
		}
		if err := validPassword(newPassword); err != nil {
			return model.User{}, err
		}
		if password == newPassword {
			return model.User{}, lifecycleFail(400, "Choose a different password.", "invalid_password")
		}
		next, err := hashPassword(newPassword)
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

// HasPassword reports whether userID has a password row, so the dashboard
// and Hack account screens know whether to show the Password card
// (GET /v1/me, PASSWORD_SIGNIN on only).
func (s *Service) HasPassword(ctx context.Context, userID string) (bool, error) {
	_, err := repo.PasswordHashByID(ctx, s.database, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// PasswordLogin reports a password account's own login for display (GET
// /v1/me, instead of a self sign-up's possibly-synthetic users.username).
// ok=false when the account has no password row.
func (s *Service) PasswordLogin(ctx context.Context, userID string) (string, bool, error) {
	login, err := repo.PasswordLoginByID(ctx, s.database, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return login, err == nil, err
}

// SelfSignupPasswordAccount reports whether userID was created through the
// public password sign-up form, never admin-created: see
// repo.SelfSignupPasswordAccount.
func (s *Service) SelfSignupPasswordAccount(ctx context.Context, userID string) (bool, error) {
	return repo.SelfSignupPasswordAccount(ctx, s.database, userID)
}

// ChangePassword lets a signed-in password account replace its own password:
// current must verify, next must be a different valid password. Same
// verify-then-lock-and-compare-hash pattern as PasswordSignIn, so a password
// changed between the two reads is rejected rather than silently overwritten.
// It never touches keys or sessions; every other sign-in stays valid.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	hash, err := repo.PasswordHashByID(ctx, s.database, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecycleFail(404, "not found", "")
	}
	if err != nil {
		return err
	}
	bounded := current
	if len(bounded) > 1024 {
		bounded = bounded[:1024]
	}
	matches := verifyPassword(bounded, hash)
	if !matches || validPassword(current) != nil {
		return changePasswordBadCredentials()
	}
	if err := validPassword(next); err != nil {
		return err
	}
	if current == next {
		return lifecycleFail(400, "Choose a different password.", "invalid_password")
	}
	nextHash, err := hashPassword(next)
	if err != nil {
		return err
	}
	tx, err := s.database.BeginTx(ctx, nil)
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
func temporaryPassword(password string) (string, error) {
	if password == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		password = base64.RawURLEncoding.EncodeToString(b)
	}
	return password, validPassword(password)
}

// CreatePasswordAccount creates a password account for login (what the
// person typed, or an admin-chosen address for temporary=true). login is
// stored, normalized, as account_passwords.login, which is where
// PasswordAccount looks a login up and where its own uniqueness is checked
// -- not users.username.
//
// Self sign-up (temporary=false, signup_source "website") never proved it
// controls login, even one shaped like an email, so users.username becomes
// a synthetic, never-email value instead (randomPasswordUsername) and login
// is handed to assign for handle derivation only (SanitizeHandleBase already
// strips a domain, so no address leaks into the handle). Admin create
// (temporary=true, signup_source "admin") keeps users.username == login --
// the admin vouches for that address, same as before this change.
func (s *Service) CreatePasswordAccount(ctx context.Context, login, password string, temporary, randomHandles bool, assign func(context.Context, AttributionStore, string, string, bool)) (model.User, string, error) {
	login = NormalizeLogin(login)
	if err := validLogin(login); err != nil {
		return model.User{}, "", err
	}
	var err error
	if temporary {
		password, err = temporaryPassword(password)
	} else {
		err = validPassword(password)
	}
	if err != nil {
		return model.User{}, "", err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, "", err
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, "", err
	}
	defer tx.Rollback()
	// Use the same insertion and handle-assignment path as bulk account creation.
	source, username := "website", login
	if temporary {
		source = "admin"
	} else if username, err = randomPasswordUsername(); err != nil {
		return model.User{}, "", err
	}
	var id string
	for attempt := 0; ; attempt++ {
		id, err = repo.InsertAccount(ctx, tx, username, source, "password")
		if !temporary && errors.Is(err, sql.ErrNoRows) && attempt < 4 {
			// The random synthetic username collided (vanishingly
			// unlikely): try a fresh one. This can never be login's own
			// uniqueness -- that is checked below, against
			// account_passwords.login, not users.username.
			if username, err = randomPasswordUsername(); err != nil {
				return model.User{}, "", err
			}
			continue
		}
		break
	}
	if errors.Is(err, sql.ErrNoRows) || repo.IsUniqueViolation(err) {
		return model.User{}, "", lifecycleFail(409, "That username is taken.", "username_taken")
	}
	if err != nil {
		return model.User{}, "", err
	}
	assign(ctx, tx, id, login, randomHandles)
	user, err := repo.GetUserByID(ctx, tx, id)
	if err != nil {
		return user, "", err
	}
	if !user.Handle.Valid {
		return user, "", lifecycleFail(500, "could not assign account handle", "")
	}
	if err := repo.SetPassword(ctx, tx, id, hash, login, temporary); err != nil {
		if repo.IsUniqueViolation(err) {
			// login already belongs to another password account
			// (account_passwords.login is unique) -- the same outward
			// refusal as a username clash, whichever table actually
			// caught it.
			return user, "", lifecycleFail(409, "That username is taken.", "username_taken")
		}
		return user, "", err
	}

	return user, password, tx.Commit()
}
func (s *Service) ResetPassword(ctx context.Context, id, password string) (model.User, string, error) {
	password, err := temporaryPassword(password)
	if err != nil {
		return model.User{}, "", err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, "", err
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, "", err
	}
	defer tx.Rollback()
	user, err := repo.LockReissueAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return user, "", lifecycleFail(404, "not found", "")
	}
	if err != nil {
		return user, "", err
	}
	if user.IsAdmin {
		return user, "", lifecycleFail(400, "cannot set an admin password", "")
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
	if err := repo.SetPassword(ctx, tx, id, hash, NormalizeLogin(user.Username), true); err != nil {
		return user, "", err
	}
	return user, password, tx.Commit()
}
