package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

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

// historySize is what one history row holds: the text of its value or diff,
// the same measure the sites.history_bytes trigger keeps
// (sd1-saved-data-safety3-history-bytes.sql).
const historySize = `COALESCE(octet_length(prev::text), octet_length(diff::text), 0)`

// ThinSiteHistory removes older non-keeper rows until the cap is met.
func ThinSiteHistory(ctx context.Context, database *sql.DB, siteID string, capBytes int64) (int64, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM data_history WHERE id IN (
			SELECT id FROM (
				SELECT id, keeper, sz,
				       sum(sz) OVER (ORDER BY keeper DESC, id DESC ROWS UNBOUNDED PRECEDING) AS running
				  FROM (
					SELECT id, `+historySize+` AS sz,
					       (kind <> 'state' OR prev IS NOT NULL) AND row_number() OVER (
					         PARTITION BY kind, name, item_id, (created_at AT TIME ZONE 'UTC')::date,
					                      (kind <> 'state' OR prev IS NOT NULL)
					         ORDER BY id) = 1 AS keeper
					  FROM data_history WHERE site_id = $1
				  ) s
			) t
			WHERE NOT keeper AND sz > 0 AND running > $2
		)`, siteID, capBytes)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeDeletedItems removes the corresponding saved data.
func PurgeDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64) (int64, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3::bigint = 0 OR id = $3)`, siteID, collection, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearSiteHistory removes the corresponding saved data.
func ClearSiteHistory(ctx context.Context, database *sql.DB, siteID string) (int64, error) {
	res, err := database.ExecContext(ctx, `DELETE FROM data_history WHERE site_id = $1`, siteID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListHistory returns one target’s changes newest first.
func ListHistory(ctx context.Context, database *sql.DB, siteID, kind, name string, limit int, before int64) ([]model.HistoryEntry, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, item_id, op, `+historyByExpr+`, actor_kind, created_at,
		       COALESCE(octet_length(prev::text), octet_length(diff::text), 0)
		  FROM data_history
		 WHERE site_id = $1 AND kind = $2 AND name = $3 AND ($4::bigint = 0 OR id < $4)
		 ORDER BY id DESC
		 LIMIT $5`, siteID, kind, name, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.HistoryEntry, 0)
	for rows.Next() {
		var e model.HistoryEntry
		var item sql.NullInt64
		if err := rows.Scan(&e.ID, &item, &e.Op, &e.By, &e.ByKind, &e.At, &e.Size); err != nil {
			return nil, err
		}
		if item.Valid {
			v := item.Int64
			e.ItemID = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HistoryRecord reads a change and its stored previous value.
func HistoryRecord(ctx context.Context, q sqlkit.Querier, siteID, kind, name string, id int64) (model.HistoryEntry, []byte, error) {
	var e model.HistoryEntry
	var item sql.NullInt64
	var prev []byte
	err := q.QueryRowContext(ctx, `
		SELECT id, item_id, op, `+historyByExpr+`, actor_kind, created_at,
		       COALESCE(octet_length(prev::text), octet_length(diff::text), 0), prev
		  FROM data_history
		 WHERE id = $1 AND site_id = $2 AND kind = $3 AND name = $4`, id, siteID, kind, name).
		Scan(&e.ID, &item, &e.Op, &e.By, &e.ByKind, &e.At, &e.Size, &prev)
	if err != nil {
		return e, nil, err
	}
	if item.Valid {
		v := item.Int64
		e.ItemID = &v
	}
	return e, prev, nil
}

// LockState locks and reads a site’s current document.
func LockState(ctx context.Context, q *sql.Tx, siteID string) (json.RawMessage, int, int64, error) {
	var state []byte
	var version int
	var size int64
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(state, 'null'::jsonb), state_version, state_bytes FROM sites WHERE id = $1 FOR UPDATE`, siteID).
		Scan(&state, &version, &size)
	if err != nil {
		return nil, 0, 0, err
	}
	return json.RawMessage(state), version, size, nil
}

// UpdateLockedState updates a locked document and returns its measured sizes.
func UpdateLockedState(ctx context.Context, q *sql.Tx, siteID string, state json.RawMessage) (int, int64, int64, error) {
	var version int
	var size, total int64
	err := q.QueryRowContext(ctx, `
		UPDATE sites SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		 WHERE id = $1
		RETURNING state_version, state_bytes, data_bytes`, siteID, string(state)).Scan(&version, &size, &total)
	return version, size, total, err
}

// HistoryValueFlags reports whether a change has a previous value or diff.
func HistoryValueFlags(ctx context.Context, q sqlkit.Querier, siteID string, id int64) (bool, bool, error) {
	var hasPrev, hasDiff bool
	err := q.QueryRowContext(ctx, `
		SELECT prev IS NOT NULL, diff IS NOT NULL FROM data_history
		 WHERE id = $1 AND site_id = $2 AND kind = 'state' AND name = ''`, id, siteID).Scan(&hasPrev, &hasDiff)
	return hasPrev, hasDiff, err
}

// NextFullHistoryID finds the nearest full history row at or after id.
func NextFullHistoryID(ctx context.Context, q sqlkit.Querier, siteID string, id int64) (sql.NullInt64, error) {
	var fullID sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT min(id) FROM data_history
		 WHERE site_id = $1 AND kind = 'state' AND name = '' AND id >= $2 AND prev IS NOT NULL`, siteID, id).Scan(&fullID)
	return fullID, err
}

// FullHistoryValue reads a full history value into base.
func FullHistoryValue(ctx context.Context, q sqlkit.Querier, id int64, base *[]byte) error {
	return q.QueryRowContext(ctx, `SELECT prev FROM data_history WHERE id = $1`, id).Scan(base)
}

// CurrentStateValue reads the site's current state into base.
func CurrentStateValue(ctx context.Context, q sqlkit.Querier, siteID string, base *[]byte) error {
	return q.QueryRowContext(ctx, `SELECT COALESCE(state, 'null'::jsonb) FROM sites WHERE id = $1`, siteID).Scan(base)
}

// HistoryDiffRows returns reverse diffs newest first in the requested range.
func HistoryDiffRows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, siteID string, id, upper int64) (*sql.Rows, error) {
	return q.QueryContext(ctx, `
		SELECT diff FROM data_history
		 WHERE site_id = $1 AND kind = 'state' AND name = '' AND id >= $2 AND id < $3
		 ORDER BY id DESC`, siteID, id, upper)
}

// FullStateSnapshots reports whether recent history contains full snapshots.
func FullStateSnapshots(ctx context.Context, tx *sql.Tx, siteID string, snapshotEvery int) (bool, bool, error) {
	var fullToday, fullRecent bool
	err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(bool_or(is_full AND today), false), COALESCE(bool_or(is_full), false) FROM (
				SELECT prev IS NOT NULL AS is_full,
				       (created_at AT TIME ZONE 'UTC')::date = (now() AT TIME ZONE 'UTC')::date AS today
				  FROM data_history WHERE site_id = $1 AND kind = 'state' AND name = ''
				 ORDER BY id DESC LIMIT $2) t`, siteID, snapshotEvery).Scan(&fullToday, &fullRecent)
	return fullToday, fullRecent, err
}

// InsertStateDiff records a reverse diff for a state change.
func InsertStateDiff(ctx context.Context, tx *sql.Tx, siteID, op string, diff []byte, a model.Actor) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO data_history (site_id, kind, name, op, diff, actor_id, actor_kind, actor_email)
		VALUES ($1, 'state', '', $2, $3::jsonb, $4, $5, $6)`,
		siteID, op, string(diff), nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}

// SoftClearCollection marks live list items deleted and records the clears.
func SoftClearCollection(ctx context.Context, database *sql.DB, siteID, collection string, a model.Actor) (int64, error) {
	var n int64
	err := database.QueryRowContext(ctx, `
		WITH gone AS (
			UPDATE collection_items SET deleted_at = now()
			 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
			RETURNING id
		), hist AS (
			INSERT INTO data_history (site_id, kind, name, item_id, op, actor_id, actor_kind, actor_email)
			SELECT $1, 'list', $2, id, 'clear', $3, $4, $5 FROM gone
		)
		SELECT count(*) FROM gone`, siteID, collection, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email)).Scan(&n)
	return n, err
}

// UndeleteItems restores the requested items and records their restoration.
func UndeleteItems(ctx context.Context, tx *sql.Tx, siteID, collection string, id int64, a model.Actor, onlyCleared bool, within time.Duration) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `
		WITH back AS (
			UPDATE collection_items SET deleted_at = NULL
			 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3::bigint = 0 OR id = $3)
			   AND (NOT $7 OR (SELECT h.op FROM data_history h WHERE h.item_id = collection_items.id
			                    ORDER BY h.id DESC LIMIT 1) = 'clear')
			   AND ($8 = 0 OR deleted_at >= now() - make_interval(secs => $8))
			RETURNING id
		), hist AS (
			INSERT INTO data_history (site_id, kind, name, item_id, op, actor_id, actor_kind, actor_email)
			SELECT $1, 'list', $2, id, 'undelete', $4, $5, $6 FROM back
		)
		SELECT count(*) FROM back`, siteID, collection, id, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email), onlyCleared, int64(within.Seconds())).Scan(&n)
	return n, err
}

// LockHistoryName locks a declaration and returns its kind.
func LockHistoryName(ctx context.Context, tx *sql.Tx, siteID, name string) (string, error) {
	var kind string
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(kind, '') FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// LockHistoryItem locks an item and reads its data and deletion state.
func LockHistoryItem(ctx context.Context, tx *sql.Tx, itemID int64, siteID, collection string) (json.RawMessage, sql.NullTime, error) {
	var cur json.RawMessage
	var deleted sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT data, deleted_at FROM collection_items
		 WHERE id = $1 AND site_id = $2 AND collection = $3 FOR UPDATE`, itemID, siteID, collection).Scan(&cur, &deleted)
	return cur, deleted, err
}

// UndeleteHistoryItem brings an item back during a history restore.
func UndeleteHistoryItem(ctx context.Context, tx *sql.Tx, itemID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE collection_items SET deleted_at = NULL WHERE id = $1`, itemID)
	return err
}

// RestoreHistoryItem restores an item’s earlier value.
func RestoreHistoryItem(ctx context.Context, tx *sql.Tx, itemID int64, value json.RawMessage) error {
	_, err := tx.ExecContext(ctx, `UPDATE collection_items SET data = $2::jsonb, deleted_at = NULL, version = version + 1 WHERE id = $1`, itemID, string(value))
	return err
}

// HistoryItem reads the restored item.
func HistoryItem(ctx context.Context, tx *sql.Tx, itemID int64) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `SELECT id, data, created_at, version FROM collection_items WHERE id = $1`, itemID).Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	return it, err
}

// PurgeOldDeletedItems removes items outside the undo window.
func PurgeOldDeletedItems(ctx context.Context, database *sql.DB, undoDays int) (sql.Result, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM collection_items WHERE deleted_at IS NOT NULL AND deleted_at < now() - make_interval(days => $1)`, undoDays)
	return res, err
}

// PurgeOldHistory removes history outside the undo window.
func PurgeOldHistory(ctx context.Context, database *sql.DB, undoDays int) (sql.Result, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM data_history h
		 WHERE h.created_at < now() - make_interval(days => $1)
		   AND (h.kind <> 'state' OR NOT EXISTS (
		        SELECT 1 FROM data_history k
		         WHERE k.site_id = h.site_id AND k.kind = 'state' AND k.name = h.name
		           AND k.id < h.id AND k.created_at >= now() - make_interval(days => $1)))`, undoDays)
	return res, err
}

// ListChangesElsewhere returns changes made by a person on other sites.
func ListChangesElsewhere(ctx context.Context, database *sql.DB, userID string) ([]model.ChangeElsewhere, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT h.site_id::text, h.kind, h.name, h.item_id, h.op, h.created_at
		  FROM data_history h JOIN sites s ON s.id = h.site_id
		 WHERE h.actor_id = $1 AND s.user_id IS DISTINCT FROM $1
		 ORDER BY h.created_at, h.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ChangeElsewhere
	for rows.Next() {
		var c model.ChangeElsewhere
		var item sql.NullInt64
		if err := rows.Scan(&c.SiteID, &c.Kind, &c.Name, &item, &c.Op, &c.At); err != nil {
			return nil, err
		}
		if item.Valid {
			v := item.Int64
			c.ItemID = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
