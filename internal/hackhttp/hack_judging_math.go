package hackhttp

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/db"
)

func computeResultsWithOptions(ctx context.Context, q db.Querier, ev db.Event) ([]teamResult, error) {
	return entries.New(nil, nil).ComputeResults(ctx, q, ev)
}
