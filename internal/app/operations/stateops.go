package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
)

// PatchSiteState runs a PATCH in one transaction. apply gets the document,
// read with the site row locked, and returns the patch or an error, which is
// passed back as is. The change goes to history as a reverse diff, or as a
// full copy on the day's first change, when none of the last snapshotEvery
// changes is one, or when the diff would be about as large. ErrSiteFull when
// the document grew and the site is past maxBytes. Returns the new version.
func (s *Reporting) PatchSiteState(ctx context.Context, siteID string, a model.Actor, maxBytes int64, snapshotEvery int, apply func(cur json.RawMessage) (model.StatePatch, error)) (int, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cur, _, size, err := repo.LockState(ctx, tx, siteID)
	if err != nil {
		return 0, err
	}
	p, err := apply(cur)
	if err != nil {
		return 0, err
	}
	ver, err := setStateWithin(ctx, tx, siteID, p.Next, size, maxBytes)
	if err != nil {
		return 0, err
	}
	if err := repo.RecordStatePatch(ctx, tx, siteID, cur, p, snapshotEvery, a); err != nil {
		return 0, err
	}
	return ver, tx.Commit()
}

func setStateWithin(ctx context.Context, tx *sql.Tx, siteID string, state json.RawMessage, oldSize, maxBytes int64) (int, error) {
	ver, size, total, err := repo.UpdateLockedState(ctx, tx, siteID, state)
	if err != nil {
		return 0, err
	}
	if maxBytes > 0 && size > oldSize && total > maxBytes {
		return 0, model.ErrSiteFull
	}
	return ver, nil
}
func (s *Reporting) SiteState(ctx context.Context, siteID string) (json.RawMessage, int, error) {
	return sites.GetSiteStateByID(ctx, s.database, siteID)
}
