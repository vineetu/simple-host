package operations

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ProductImages handles jobs whose Kind the product owns. Nil means no product.
// Calls keep quarantine's order: describe, record, evidence, clear, take-down,
// report, alert. A failed evidence copy skips Clear, but still takes the group down.
type ProductImages interface {
	Owns(kind string) bool
	Load(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob) ([]byte, error)
	// Describe runs once per match, before its row is written. found=false with
	// a nil error means the owning group no longer exists.
	Describe(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob) (d ProductImageOwner, found bool, err error)
	// Clear removes the stored bytes only while they still hash to sha.
	Clear(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob, sha string) (cleared bool, err error)
	// TakeDown commits the group's take-down and holding-account suspension in
	// their existing transaction order, then synchronises its disk markers.
	TakeDown(ctx context.Context, job model.ImageScanJob, message string) error
}

type ProductImageOwner struct {
	Label      string // ImageMatch.EventSlug
	OwnerID    string // ImageMatch.OwnerID
	OwnerEmail string // ImageMatch.OwnerEmail
}
