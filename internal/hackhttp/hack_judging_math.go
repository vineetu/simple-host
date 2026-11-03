package hackhttp

import (
	"context"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/model"
)

func computeResultsWithOptions(ctx context.Context, q entries.Querier, ev model.Event) ([]teamResult, error) {
	return entries.New(nil, nil).ComputeResults(ctx, q, ev)
}
