package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// HistoryBytes reads the tracked history size, treating a missing site as empty.
func HistoryBytes(ctx context.Context, q sqlkit.Querier, siteID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT history_bytes FROM sites WHERE id = $1`, siteID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// SitesOverHistoryCap lists sites whose tracked history exceeds the cap.
func SitesOverHistoryCap(ctx context.Context, database *sql.DB, capBytes int64) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id::text FROM sites WHERE history_bytes > $1`, capBytes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// HasStateHistory reports whether the document has any recorded change.
func HasStateHistory(ctx context.Context, database *sql.DB, siteID string) (bool, error) {
	var ok bool
	err := database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM data_history WHERE site_id = $1 AND kind = 'state' AND name = '')`, siteID).Scan(&ok)
	return ok, err
}

// RecordStateChange records the previous state when the document changed.
func RecordStateChange(ctx context.Context, q sqlkit.Querier, siteID, op string, prev, next json.RawMessage, a model.Actor) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO data_history (site_id, kind, name, op, prev, actor_id, actor_kind, actor_email)
		SELECT $1, 'state', '', $2, $3::jsonb, $5, $6, $7
		 WHERE $3::jsonb IS DISTINCT FROM $4::jsonb`,
		siteID, op, string(prev), string(next), nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}
