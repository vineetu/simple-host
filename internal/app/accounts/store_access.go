package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// GetUserByHandle looks up a user by their URL-safe handle.
func (svc *Service) GetUserByHandle(ctx context.Context, handle string) (model.User, error) {
	return repo.GetUserByHandle(ctx, svc.database, handle)
}

// GetUserByHandleOrAlias looks a handle up, falling back to an old handle kept
// as an alias. Returns sql.ErrNoRows when neither exists.
func (svc *Service) GetUserByHandleOrAlias(ctx context.Context, handle string) (model.User, error) {
	return repo.GetUserByHandleOrAlias(ctx, svc.database, handle)
}

// ResolveHandleAlias returns the current handle of the account that used to
// be old, or sql.ErrNoRows.
func (svc *Service) ResolveHandleAlias(ctx context.Context, old string) (string, error) {
	return repo.ResolveHandleAlias(ctx, svc.database, old)
}
