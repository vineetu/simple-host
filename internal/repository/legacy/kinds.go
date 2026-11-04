package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// CountLiveItems reads the corresponding collection count or existence.
func CountLiveItems(ctx context.Context, q sqlkit.Querier, siteID, name string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL`, siteID, name).Scan(&n)
	return n, err
}

// NameHasRows reads the corresponding collection count or existence.
func NameHasRows(ctx context.Context, q sqlkit.Querier, siteID, name string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM collection_items WHERE site_id = $1 AND collection = $2)`, siteID, name).Scan(&ok)
	return ok, err
}

// CountKindNames reads the declared data.
func CountKindNames(ctx context.Context, q sqlkit.Querier, siteID, kind, except string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM collection_settings WHERE site_id = $1 AND kind = $2 AND collection <> $3`, siteID, kind, except).Scan(&n)
	return n, err
}

// GetContent reads the declared data.
func GetContent(ctx context.Context, q sqlkit.Querier, siteID, name string) (model.CollectionItem, bool, error) {
	var it model.CollectionItem
	err := q.QueryRowContext(ctx, `
		SELECT id, data, created_at FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
		 ORDER BY id DESC LIMIT 1`, siteID, name).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	return it, err == nil, err
}

// ListOwnEntries reads the declared data.
func ListOwnEntries(ctx context.Context, database *sql.DB, siteID, name, userID string, limit int, before int64) ([]model.CollectionItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, data, created_at FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3 AND deleted_at IS NULL
		   AND ($4::bigint = 0 OR id < $4)
		 ORDER BY id DESC LIMIT $5`, siteID, name, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.CollectionItem, 0)
	for rows.Next() {
		var it model.CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetPersonal reads the person’s record.
func GetPersonal(ctx context.Context, q sqlkit.Querier, siteID, name, userID string) (model.CollectionItem, bool, error) {
	var it model.CollectionItem
	err := q.QueryRowContext(ctx, `
		SELECT id, data, created_at, version FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3 AND deleted_at IS NULL
		 ORDER BY id DESC LIMIT 1`, siteID, name, userID).Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	return it, err == nil, err
}

// PersonalItemID reads the person’s record.
func PersonalItemID(ctx context.Context, q sqlkit.Querier, siteID, name, userID string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT id FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3
		 ORDER BY id DESC LIMIT 1`, siteID, name, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ItemAuthor is a shared legacy record.
type ItemAuthor = model.ItemAuthor

// GetItemAuthor reads who sent item id of name on siteID: the address the
// server stamped when it was saved. Only a private list's _submitted_by
// (stamped by the server too) stands in for rows saved before that column;
// a public list's is whatever the page sent, so it is never used.
func GetItemAuthor(ctx context.Context, q sqlkit.Querier, siteID, name string, id int64) (ItemAuthor, error) {
	var a ItemAuthor
	var by, email sql.NullString
	var del sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT ci.submitted_by::text,
		       COALESCE(ci.submitted_email, CASE WHEN ci.submitted_by IS NOT NULL AND COALESCE(cs.private, false)
		                                         THEN ci.data->>'_submitted_by' END),
		       ci.deleted_at
		  FROM collection_items ci
		  LEFT JOIN collection_settings cs ON cs.site_id = ci.site_id AND cs.collection = ci.collection
		 WHERE ci.id = $1 AND ci.site_id = $2 AND ci.collection = $3`, id, siteID, name).Scan(&by, &email, &del)
	if err != nil {
		return a, err
	}
	a.SubmittedBy, a.Email = by.String, email.String
	if del.Valid {
		t := del.Time
		a.DeletedAt = &t
	}
	return a, nil
}

// NotifyDue is a shared legacy record.
type NotifyDue = model.NotifyDue

// DueNotifications lists every name with notify each or daily whose interval
// has passed and that has new entries from people other than the owner since
// the last email (or since it was declared). Taken-down, offline and deleted
// sites are skipped. "Since" is by id once an email recorded one: entries of
// a Submissions name go in one at a time under the name's lock, so their ids
// are in commit order, while created_at (a transaction's start) is not, and
// an entry committed just after one digest read would be missed by every
// later one.
func DueNotifications(ctx context.Context, database *sql.DB, each, daily time.Duration) ([]NotifyDue, error) {
	rows, err := database.QueryContext(ctx, `
		WITH due AS (
			SELECT cs.site_id, cs.collection, cs.notify, cs.notify_sent_at, cs.notify_last_id,
			       COALESCE(cs.notify_sent_at, cs.declared_at, cs.updated_at) AS since
			  FROM collection_settings cs
			 WHERE cs.kind = 'entries' AND cs.notify IN ('each', 'daily')
			   AND COALESCE(cs.notify_sent_at, cs.declared_at, cs.updated_at) <=
			       now() - CASE WHEN cs.notify = 'each' THEN $1::interval ELSE $2::interval END
		)
		SELECT d.site_id, s.name, COALESCE(u.handle, ''), s.user_id, d.collection, d.notify, d.since, d.notify_sent_at, now(),
		       count(ci.id), max(ci.id)
		  FROM due d
		  JOIN sites s ON s.id = d.site_id AND s.deleted_at IS NULL AND s.suspended_at IS NULL AND s.offline_at IS NULL
		  JOIN users u ON u.id = s.user_id
		  JOIN collection_items ci ON ci.site_id = d.site_id AND ci.collection = d.collection
		   AND ci.deleted_at IS NULL
		   AND CASE WHEN d.notify_last_id IS NULL THEN ci.created_at > d.since ELSE ci.id > d.notify_last_id END
		   AND ci.submitted_by IS DISTINCT FROM s.user_id
		 GROUP BY d.site_id, s.name, u.handle, s.user_id, d.collection, d.notify, d.since, d.notify_sent_at`,
		each.String(), daily.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyDue
	for rows.Next() {
		var n NotifyDue
		var sent sql.NullTime
		if err := rows.Scan(&n.SiteID, &n.SiteName, &n.Handle, &n.OwnerID, &n.Name, &n.Notify, &n.Since, &sent, &n.CheckUntil, &n.Count, &n.LastID); err != nil {
			return nil, err
		}
		if sent.Valid {
			t := sent.Time
			n.SentAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ClaimNotification records, before the email goes out, that the owner is
// emailed about d's entries up to d.CheckUntil and d.LastID. Only one caller wins it (the
// row still holds the notify_sent_at that DueNotifications read), so two
// servers never both send, and a failed send is not retried every tick.
func ClaimNotification(ctx context.Context, database *sql.DB, d NotifyDue) (bool, error) {
	res, err := database.ExecContext(ctx, `
		UPDATE collection_settings SET notify_sent_at = $3, notify_last_id = GREATEST(COALESCE(notify_last_id, 0), $5)
		 WHERE site_id = $1 AND collection = $2 AND notify_sent_at IS NOT DISTINCT FROM $4`,
		d.SiteID, d.Name, d.CheckUntil, d.SentAt, d.LastID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetNotify changes only a name's email choice (the stop link).
func SetNotify(ctx context.Context, database *sql.DB, siteID, name, notify string) (bool, error) {
	res, err := database.ExecContext(ctx, `
		UPDATE collection_settings SET notify = $3, updated_at = now() WHERE site_id = $1 AND collection = $2 AND kind = 'entries'`, siteID, name, notify)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Savers is a shared legacy record.
type Savers = model.Savers

// GetSavers reads a site's who-may-save setting (sorted lists).
func GetSavers(ctx context.Context, database *sql.DB, siteID string) (Savers, error) {
	s := Savers{Mode: "anyone", Allow: []string{}, Block: []string{}}
	if err := database.QueryRowContext(ctx, `SELECT savers_mode FROM sites WHERE id = $1`, siteID).Scan(&s.Mode); err != nil {
		return s, err
	}
	rows, err := database.QueryContext(ctx, `SELECT list, pattern FROM site_savers WHERE site_id = $1 ORDER BY pattern`, siteID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var list, p string
		if err := rows.Scan(&list, &p); err != nil {
			return s, err
		}
		if list == "allow" {
			s.Allow = append(s.Allow, p)
		} else {
			s.Block = append(s.Block, p)
		}
	}
	return s, rows.Err()
}

// SetSavers replaces a site's who-may-save setting. Patterns are already
// normalised by the caller.
func SetSavers(ctx context.Context, tx *sql.Tx, siteID string, s Savers) error {
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET savers_mode = $2 WHERE id = $1`, siteID, s.Mode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_savers WHERE site_id = $1`, siteID); err != nil {
		return err
	}
	for list, ps := range map[string][]string{"allow": s.Allow, "block": s.Block} {
		if len(ps) == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_savers (site_id, list, pattern)
			SELECT $1, $2, unnest($3::text[]) ON CONFLICT DO NOTHING`, siteID, list, pq.Array(ps)); err != nil {
			return err
		}
	}
	return nil
}

// AddBlocked adds one pattern to a site's block list, and returns how many
// patterns the list now holds.
func AddBlocked(ctx context.Context, database *sql.DB, siteID, pattern string) (int, error) {
	if _, err := database.ExecContext(ctx, `
		INSERT INTO site_savers (site_id, list, pattern) VALUES ($1, 'block', $2) ON CONFLICT DO NOTHING`, siteID, pattern); err != nil {
		return 0, err
	}
	var n int
	err := database.QueryRowContext(ctx, `SELECT count(*) FROM site_savers WHERE site_id = $1 AND list = 'block'`, siteID).Scan(&n)
	return n, err
}

// SaverMode reads the site's saver mode and whether any patterns exist.
func SaverMode(ctx context.Context, database *sql.DB, siteID string) (mode string, any bool, err error) {
	err = database.QueryRowContext(ctx, `
		SELECT savers_mode, EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1) FROM sites WHERE id = $1`, siteID).Scan(&mode, &any)
	return
}

// SaverMatches checks the allow and block patterns.
func SaverMatches(ctx context.Context, database *sql.DB, siteID string, keys, blockKeys []string) (blocked, allowed bool, err error) {
	err = database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'block' AND pattern = ANY($3)),
		       EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'allow' AND pattern = ANY($2))`,
		siteID, pq.Array(keys), pq.Array(blockKeys)).Scan(&blocked, &allowed)
	return
}

// UpdatePrivate updates a declaration's visibility under its lock.
func UpdatePrivate(ctx context.Context, tx *sql.Tx, siteID, name string, private bool) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE collection_settings SET private = $3, updated_at = now() WHERE site_id = $1 AND collection = $2`, siteID, name, private)
	return err
}

// LockContentDeclaration locks an existing declaration before a write.
func LockContentDeclaration(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	var one int
	return tx.QueryRowContext(ctx, `
			SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&one)
}

// LockContentForPut locks a declaration before replacing its content.
func LockContentForPut(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	var one int
	return tx.QueryRowContext(ctx, `
		SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&one)
}

// LockWithdrawal reads and locks a withdrawn item and its latest history.
func LockWithdrawal(ctx context.Context, tx *sql.Tx, siteID, name string, id int64, userID string, anyAuthor bool) (op, actor sql.NullString, at time.Time, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT h.op, h.actor_id::text, h.created_at
		  FROM collection_items ci
		  JOIN data_history h ON h.item_id = ci.id
		 WHERE ci.id = $1 AND ci.site_id = $2 AND ci.collection = $3 AND ($5 OR ci.submitted_by = $4)
		   AND ci.deleted_at IS NOT NULL
		 ORDER BY h.id DESC LIMIT 1
		 FOR UPDATE OF ci`, id, siteID, name, userID, anyAuthor).Scan(&op, &actor, &at)
	return
}

// RestoreWithdrawal restores a locked withdrawn item.
func RestoreWithdrawal(ctx context.Context, tx *sql.Tx, id int64) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
		UPDATE collection_items SET deleted_at = NULL WHERE id = $1 RETURNING id, data, created_at, version`, id).
		Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	return it, err
}

// UpdateContent replaces a locked content item.
func UpdateContent(ctx context.Context, tx *sql.Tx, id int64, data json.RawMessage, a model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
			UPDATE collection_items SET data = $2::jsonb, submitted_by = $3, submitted_email = $4
			 WHERE id = $1 RETURNING id, data, created_at`, id, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).
		Scan(&it.ID, &it.Data, &it.CreatedAt)
	return it, err
}

// InsertContent creates a content item under the declaration lock.
func InsertContent(ctx context.Context, tx *sql.Tx, siteID, name string, data json.RawMessage, a model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
			INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
			VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id, data, created_at`,
		siteID, name, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	return it, err
}

// LockPersonalDeclaration locks a Personal declaration.
func LockPersonalDeclaration(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	var one int
	return tx.QueryRowContext(ctx, `
		SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2 AND kind = 'mine' FOR UPDATE`, siteID, name).Scan(&one)
}

// LockPersonalItem reads and locks a person's latest record.
func LockPersonalItem(ctx context.Context, tx *sql.Tx, siteID, name, userID string) (id int64, old json.RawMessage, deleted sql.NullTime, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT id, data, deleted_at FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3
		 ORDER BY id DESC LIMIT 1 FOR UPDATE`, siteID, name, userID).Scan(&id, &old, &deleted)
	return
}

// UpdatePersonalItem updates a locked Personal record.
func UpdatePersonalItem(ctx context.Context, tx *sql.Tx, id int64, data json.RawMessage, email string) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
			UPDATE collection_items SET data = $2::jsonb, deleted_at = NULL, version = version + 1, submitted_email = $3
			 WHERE id = $1 RETURNING id, data, created_at, version`, id, string(data), nullIfEmpty(email)).
		Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	return it, err
}

// CountPersonalItems counts records up to the supplied limit.
func CountPersonalItems(ctx context.Context, tx *sql.Tx, siteID, name string, limit int) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
				SELECT count(*) FROM (SELECT 1 FROM collection_items
				  WHERE site_id = $1 AND collection = $2 LIMIT $3) x`, siteID, name, limit).Scan(&n)
	return n, err
}

// InsertPersonalItem creates a Personal record.
func InsertPersonalItem(ctx context.Context, tx *sql.Tx, siteID, name string, data json.RawMessage, a model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
			INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
			VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id, data, created_at, version`,
		siteID, name, string(data), a.ID, nullIfEmpty(a.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	return it, err
}

// LockClearedPersonal reads and locks the most recent deleted record.
func LockClearedPersonal(ctx context.Context, tx *sql.Tx, siteID, name, userID string) (id int64, op sql.NullString, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT ci.id, (SELECT h.op FROM data_history h WHERE h.item_id = ci.id ORDER BY h.id DESC LIMIT 1)
		  FROM collection_items ci
		 WHERE ci.site_id = $1 AND ci.collection = $2 AND ci.submitted_by = $3 AND ci.deleted_at IS NOT NULL
		 ORDER BY ci.id DESC LIMIT 1 FOR UPDATE OF ci`, siteID, name, userID).Scan(&id, &op)
	return
}

// HistoryEntry is a shared legacy record.
type HistoryEntry = model.HistoryEntry

const historyByExpr = `CASE WHEN actor_kind = 'admin' THEN 'Simple Host (operator)' ELSE COALESCE(actor_email, '') END || CASE WHEN actor_app <> '' THEN ' via ' || actor_app ELSE '' END`

// ListItemHistory reads an item's changes in descending order.
func ListItemHistory(ctx context.Context, database *sql.DB, siteID, name string, itemID int64, limit int, before int64) ([]HistoryEntry, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, item_id, op, `+historyByExpr+`, actor_kind, created_at,
		       COALESCE(octet_length(prev::text), octet_length(diff::text), 0)
		  FROM data_history
		 WHERE site_id = $1 AND kind = 'list' AND name = $2 AND item_id = $3 AND ($4::bigint = 0 OR id < $4)
		 ORDER BY id DESC
		 LIMIT $5`, siteID, name, itemID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HistoryEntry, 0)
	for rows.Next() {
		var e HistoryEntry
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
