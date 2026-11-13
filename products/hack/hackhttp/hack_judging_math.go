package hackhttp

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
)

func computeResultsWithOptions(ctx context.Context, q entries.Querier, ev model.Event) ([]teamResult, error) {
	return entries.New(nil, nil).ComputeResults(ctx, q, ev)
}
