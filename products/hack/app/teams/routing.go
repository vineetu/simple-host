package teams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) LoadRoutingFacts(ctx context.Context, tx *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
	return hackevents.LoadRoutingFacts(ctx, tx)
}
