package connector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
)

func ReviewerAccount(ctx context.Context, db *sql.DB, email string, randomHandles bool, assign func(context.Context, sqlkit.Querier, string, string, bool)) (model.User, error) {
	user, err := repo.GetUserByUsername(ctx, db, email)
	if errors.Is(err, sql.ErrNoRows) {
		user, err = repo.CreateUser(ctx, db, email, "", false)
		if err == nil {
			accounts.RecordSignup(ctx, db, user.ID, model.Signup{Source: model.SignupReviewer, Method: model.SignupMethodPassword})
		} else if repo.IsUniqueConstraintViolation(err) {
			user, err = repo.GetUserByUsername(ctx, db, email)
		}
	}
	if err != nil {
		return model.User{}, err
	}
	if accounts.IsOperator(user.IsAdmin, user.Username) {
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
	if pw, perr := repo.PasswordSignupAccount(ctx, db, user.ID); perr != nil {
		return model.User{}, perr
	} else if pw {
		return model.User{}, errors.New("the reviewer email is a password account; refusing")
	}
	if !user.Handle.Valid {
		assign(ctx, db, user.ID, email, randomHandles)
		if fresh, ferr := repo.GetUserByUsername(ctx, db, email); ferr == nil {
			user = fresh
		}
	}
	return user, nil
}

// ErrReviewerUnavailable: the reviewer's account could not be had (see
// ReviewerAccount); the wrapped error says why.
var ErrReviewerUnavailable = errors.New("the reviewer account is not available")

// ReviewerSignIn is the store reviewer's sign-in once their configured
// email and password matched: their ordinary account (ReviewerAccount,
// ErrReviewerUnavailable when it cannot be had) gets plain as a new
// sign-in key; earlier keys keep working.
func ReviewerSignIn(ctx context.Context, db *sql.DB, email string, randomHandles bool, plain string) (model.User, error) {
	user, err := ReviewerAccount(ctx, db, email, randomHandles, accounts.AssignHandle)
	if err != nil {
		return model.User{}, fmt.Errorf("%w: %w", ErrReviewerUnavailable, err)
	}
	return user, accounts.New(db).AddSignInKey(ctx, user.ID, plain, model.KeyNameReviewer)
}
