package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func RecordStatePatch(ctx context.Context, tx *sql.Tx, siteID string, prev json.RawMessage, p model.StatePatch, snapshotEvery int, a model.Actor) error {
	d, err := rules.LegacyReverseDiff(p.Before, p.After)
	if err != nil {
		return err
	}
	if len(d) == 0 {
		return nil
	}
	diff, err := json.Marshal(d)
	if err != nil {
		return err
	}
	full := len(diff)*2 >= len(prev)
	if !full {
		fullToday, fullRecent, err := FullStateSnapshots(ctx, tx, siteID, snapshotEvery)
		if err != nil {
			return err
		}
		full = !fullToday || !fullRecent
	}
	if full {
		return RecordHistory(ctx, tx, siteID, "state", "", nil, "change", prev, a)
	}
	err = InsertStateDiff(ctx, tx, siteID, "change", diff, a)
	return err
}
