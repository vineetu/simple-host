package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

type historyReader interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// stateBefore rebuilds the document as it was just before state change id:
// the row's own full copy, else the nearest newer full copy (or the current
// document) with each newer reverse diff applied, newest first. q must give
// one consistent view (the site row locked, or a snapshot).
func stateBefore(ctx context.Context, q historyReader, siteID string, id int64) (json.RawMessage, error) {
	hasPrev, hasDiff, err := repo.HistoryValueFlags(ctx, q, siteID, id)
	if err != nil {
		return nil, err
	}
	if !hasPrev && !hasDiff {
		return nil, model.ErrNoEarlierValue
	}
	fullID, err := repo.NextFullHistoryID(ctx, q, siteID, id)
	if err != nil {
		return nil, err
	}
	var base []byte
	upper := int64(1<<63 - 1)
	if fullID.Valid {
		if err := repo.FullHistoryValue(ctx, q, fullID.Int64, &base); err != nil {
			return nil, err
		}
		if fullID.Int64 == id {
			return json.RawMessage(base), nil
		}
		upper = fullID.Int64
	} else if err := repo.CurrentStateValue(ctx, q, siteID, &base); err != nil {
		return nil, err
	}
	doc, err := storagerules.LegacyDecodeNumbers(base)
	if err != nil {
		return nil, err
	}
	rows, err := repo.HistoryDiffRows(ctx, q, siteID, id, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var d []storagerules.LegacyDiffEntry
		if raw == nil || json.Unmarshal(raw, &d) != nil {
			return nil, storagerules.ErrLegacyBrokenHistory
		}
		if doc, err = storagerules.LegacyApplyReverse(doc, d); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	return json.RawMessage(out), err
}
