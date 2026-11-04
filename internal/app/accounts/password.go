package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

var passwordUsername = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)
var passwordEmail = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}$`)

func NormalizeLogin(login string) string { return strings.ToLower(strings.TrimSpace(login)) }
func badCredentials() error {
	return lifecycleFail(401, "Wrong username or password.", "bad_credentials")
}

// changePasswordBadCredentials is ChangePassword's "wrong current password":
// same bad_credentials code (so the handler's limiter-refund rule still
// matches it) but 403, not 401 -- the dashboard and Hack account screens
// treat any 401 on a signed-in request as an expired key and sign the
// browser out, which a mistyped current password must not do.
func changePasswordBadCredentials() error {
	return lifecycleFail(403, "Wrong username or password.", "bad_credentials")
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
		if err := repo.SetPassword(ctx, tx, user.ID, next, false); err != nil {
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
	if err := repo.SetPassword(ctx, tx, userID, nextHash, false); err != nil {
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
func (s *Service) CreatePasswordAccount(ctx context.Context, username, password string, temporary, randomHandles bool, assign func(context.Context, AttributionStore, string, string, bool)) (model.User, string, error) {
	username = NormalizeLogin(username)
	if !passwordUsername.MatchString(username) && !(temporary && len(username) <= 254 && passwordEmail.MatchString(username)) {
		return model.User{}, "", lifecycleFail(400, "Username must be 3–64 letters, numbers, dots, underscores or hyphens.", "invalid_username")
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
	source := "website"
	if temporary {
		source = "admin"
	}
	id, err := repo.InsertAccount(ctx, tx, username, source, "password")
	if errors.Is(err, sql.ErrNoRows) || repo.IsUniqueViolation(err) {
		return model.User{}, "", lifecycleFail(409, "That username is taken.", "username_taken")
	}
	if err != nil {
		return model.User{}, "", err
	}
	assign(ctx, tx, id, username, randomHandles)
	user, err := repo.GetUserByID(ctx, tx, id)
	if err != nil {
		return user, "", err
	}
	if !user.Handle.Valid {
		return user, "", lifecycleFail(500, "could not assign account handle", "")
	}
	if err := repo.SetPassword(ctx, tx, id, hash, temporary); err != nil {
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
	if err := repo.SetPassword(ctx, tx, id, hash, true); err != nil {
		return user, "", err
	}
	return user, password, tx.Commit()
}
