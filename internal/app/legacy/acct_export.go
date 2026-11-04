package legacy

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
)

func (s *Adapter) ListChangesElsewhere(ctx context.Context, id string) ([]model.ChangeElsewhere, error) {
	return repo.ListChangesElsewhere(ctx, s.database, id)
}
