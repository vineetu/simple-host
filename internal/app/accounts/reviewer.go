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
	if !user.Handle.Valid {
		assign(ctx, s.database, user.ID, email, randomHandles)
		if fresh, ferr := repo.GetUserByUsername(ctx, s.database, email); ferr == nil {
			user = fresh
		}
	}
	return user, nil
}
