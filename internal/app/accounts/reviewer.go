package accounts

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

func (s *Service) ReviewerAccount(ctx context.Context, email string, randomHandles bool, assign func(context.Context, AttributionStore, string, string, bool)) (model.User, error) {
	user, err := repo.GetUserByUsername(ctx, s.database, email)
	if errors.Is(err, sql.ErrNoRows) {
		user, err = repo.CreateUser(ctx, s.database, email, "", false)
		if err == nil {
			RecordSignup(ctx, s.database, user.ID, model.Signup{Source: model.SignupReviewer, Method: model.SignupMethodPassword})
		} else if repo.IsUniqueConstraintViolation(err) {
			user, err = repo.GetUserByUsername(ctx, s.database, email)
		}
	}
	if err != nil {
		return model.User{}, err
	}
	if user.IsAdmin || user.Username == "admin" {
		return model.User{}, errors.New("the reviewer account is an admin account; refusing")
	}
	// A CreatePasswordAccount account under the reviewer's configured
	// address -- self-signup or admin-created, either way -- never proved
	// it controls that address, same reasoning as the pre-account-hijacking
	// fix for email-code/OAuth sign-in. Without this, whoever got that
	// address a password first would be handed the reviewer's sign-in (or
	// the reviewer would be handed theirs). PasswordSignupAccount excludes
	// the reviewer's own account, tagged signup_method='password' for an
	// unrelated reason (this function's own RecordSignup call above), so a
	// second sign-in still returns the same reviewer account as always.
	// Covers the concurrent-create fallback above too, since both paths
	// land here before the key issues.
	if pw, perr := repo.PasswordSignupAccount(ctx, s.database, user.ID); perr != nil {
		return model.User{}, perr
	} else if pw {
		return model.User{}, errors.New("the reviewer email is a password account; refusing")
	}
	if !user.Handle.Valid {
		assign(ctx, s.database, user.ID, email, randomHandles)
		if fresh, ferr := repo.GetUserByUsername(ctx, s.database, email); ferr == nil {
			user = fresh
		}
	}
	return user, nil
}
