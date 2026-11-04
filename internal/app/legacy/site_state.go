package legacy

import (
	"context"
	"encoding/json"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
)

// ReplaceSiteState owns the legacy compare-and-swap and its history transaction.
func (a *Adapter) ReplaceSiteState(ctx context.Context, siteID string, state json.RawMessage, expected int, actor model.Actor, maxBytes int64) (int, error) {
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	prev, ver, size, err := repo.LockState(ctx, tx, siteID)
	if err != nil {
		return 0, err
	}
	if expected >= 0 && ver != expected {
		return 0, model.ErrStateVersionConflict
	}
	next, nextSize, total, err := repo.UpdateLockedState(ctx, tx, siteID, state)
	if err != nil {
		return 0, err
	}
	if maxBytes > 0 && nextSize > size && total > maxBytes {
		return 0, model.ErrSiteFull
	}
	if err := repo.RecordStateChange(ctx, tx, siteID, "replace", prev, state, actor); err != nil {
		return 0, err
	}
	return next, tx.Commit()
}
