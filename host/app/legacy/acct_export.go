package legacy

import (
	"context"
	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/legacy"
)

func (s *Adapter) ListChangesElsewhere(ctx context.Context, id string) ([]model.ChangeElsewhere, error) {
	return repo.ListChangesElsewhere(ctx, s.database, id)
}
