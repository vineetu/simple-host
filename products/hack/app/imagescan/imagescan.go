// Package imagescan handles Hack's event icons and entry screenshots.
package imagescan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	entries "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	events "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type Images struct {
	database    *sql.DB
	syncMarkers func(context.Context, model.Event) bool
}

var _ operations.ProductImages = (*Images)(nil)

func New(database *sql.DB, syncMarkers func(context.Context, model.Event) bool) *Images {
	return &Images{database: database, syncMarkers: syncMarkers}
}

func (*Images) Owns(kind string) bool {
	return kind == "event_icon" || kind == "entry_screenshot"
}

func (*Images) Load(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob) ([]byte, error) {
	var raw []byte
	var err error
	switch job.Kind {
	case "event_icon":
		_, raw, err = events.GetEventIcon(ctx, q, job.EventID)
	case "entry_screenshot":
		raw, _, err = entries.GetEventScreenshot(ctx, q, job.TeamID)
	default:
		return nil, fmt.Errorf("unknown kind %q", job.Kind)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

func (*Images) Describe(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob) (operations.ProductImageOwner, bool, error) {
	ev, err := events.GetEventByID(ctx, q, job.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		return operations.ProductImageOwner{}, false, nil
	}
	if err != nil {
		return operations.ProductImageOwner{}, false, err
	}
	return operations.ProductImageOwner{Label: ev.Slug, OwnerID: ev.AccountID, OwnerEmail: ev.ContactEmail}, true, nil
}

func (*Images) Clear(ctx context.Context, q sqlkit.Querier, job model.ImageScanJob, sha string) (bool, error) {
	switch job.Kind {
	case "event_icon":
		return events.ClearEventIcon(ctx, q, job.EventID, sha)
	case "entry_screenshot":
		return entries.ClearEventScreenshot(ctx, q, job.TeamID, sha)
	default:
		return false, fmt.Errorf("unknown kind %q", job.Kind)
	}
}

func (s *Images) TakeDown(ctx context.Context, job model.ImageScanJob, message string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// TakeDownEvent already returns the holding account and marker-sync fields;
	// retain the same statements and lock order without another lookup.
	ev, err := events.TakeDownEvent(ctx, tx, job.EventID, message)
	if err != nil {
		return err
	}
	if err := accounts.SetUserSuspended(ctx, tx, ev.AccountID, message); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.syncMarkers != nil && !s.syncMarkers(ctx, ev) {
		return errors.New("team site markers could not all be written")
	}
	return nil
}
