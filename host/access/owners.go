package access

import (
	"context"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// AccountByHandle loads the account, a team or a person, whose handle is handle.
func (s *Service) AccountByHandle(ctx context.Context, handle string) (model.User, error) {
	return repo.GetAccountByHandle(ctx, s.database, handle)
}
