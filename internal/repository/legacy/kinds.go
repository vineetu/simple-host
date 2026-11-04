package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"strings"
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

// ItemAuthor identifies an entry author and deletion time.
type ItemAuthor struct {
	SubmittedBy string
	Email       string
	DeletedAt   *time.Time
}

// GetItemAuthor reads the stamped author of an item.
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

// NotifyDue is one name whose owner should be emailed about new entries.
type NotifyDue struct {
	SiteID   string
	SiteName string
	Handle   string
	OwnerID  string
	Name     string
	Notify   string
	Since    time.Time
	// SentAt is notify_sent_at as read (nil before the first email): the
	// claim compares against it.
	SentAt     *time.Time
	Count      int64
	CheckUntil time.Time
	// LastID is the newest entry counted; the claim records it, and the
	// next email counts only entries after it.
	LastID int64
}

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

// BaseEmail is the address one person is known by for block lists and one
// entry per person: lower case, with a "+tag" dropped from the part before
// the @ (ann+2@example.com is ann@example.com). "" stays "".
func BaseEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email
	}
	local := email[:at]
	if plus := strings.Index(local, "+"); plus > 0 {
		local = local[:plus]
	}
	return local + email[at:]
}

// Savers is a site's who-may-save setting.
type Savers struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

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
func SetSavers(ctx context.Context, database *sql.DB, siteID string, s Savers) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	return tx.Commit()
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

// SaverAllowed decides whether the person with this verified email may save
// on the site: never when the address or its domain is blocked; with mode
// listed, only when the address or its domain is on the allow list. The
// block list matches the address with its +tag dropped too (BaseEmail); the
// allow list only the address as it is. A domain entry matches addresses at
// exactly that domain (not its subdomains), by the address a sign-in
// verified (Google's or an emailed code's). A site
// with the default setting and no block list costs one row read. An empty
// email (nobody signed in) is refused only by a listed site.
func SaverAllowed(ctx context.Context, database *sql.DB, siteID, email string) (bool, error) {
	var mode string
	var any bool
	err := database.QueryRowContext(ctx, `
		SELECT savers_mode, EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1) FROM sites WHERE id = $1`, siteID).Scan(&mode, &any)
	if err != nil {
		return false, err
	}
	if !any {
		return mode != "listed", nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return mode != "listed", nil
	}
	keys := []string{email}
	if at := strings.LastIndex(email, "@"); at >= 0 {
		keys = append(keys, email[at:])
	}
	// A block also covers the address with any +tag (ann+2@ is ann@).
	blockKeys := keys
	if base := BaseEmail(email); base != email {
		blockKeys = append([]string{base}, keys...)
	}
	var blocked, allowed bool
	err = database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'block' AND pattern = ANY($3)),
		       EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'allow' AND pattern = ANY($2))`,
		siteID, pq.Array(keys), pq.Array(blockKeys)).Scan(&blocked, &allowed)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, nil
	}
	return mode != "listed" || allowed, nil
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

// HistoryEntry is one change, newest first in lists.
type HistoryEntry struct {
	ID     int64           `json:"id"`
	ItemID *int64          `json:"item_id,omitempty"`
	Op     string          `json:"op"`
	By     string          `json:"by,omitempty"`
	ByKind string          `json:"by_kind"`
	At     time.Time       `json:"at"`
	Size   int64           `json:"size"`
	Value  json.RawMessage `json:"value,omitempty"`
}

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
