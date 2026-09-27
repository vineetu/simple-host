package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Saved data, step 2: kinds (saved-data redesign, approved 2026-09-27).
//
// A data name on a site is declared once as one kind:
//   - KindContent ("Page info"): one document the owner writes and anyone
//     reads. Stored as the one live row of collection_items for that name, so
//     it has the same history, undo and size accounting as a list item.
//   - KindEntries ("Submissions"): items visitors send. The owner reads all;
//     each visitor reads, changes and withdraws their own; everyone reads
//     them only when the owner made the name public (collection_settings.private
//     = false).
//
// A name with no kind (collection_settings.kind NULL or no row) on a
// legacy_data site behaves exactly as before the kinds; on any other site it
// takes no saves.

// The kinds a name can be declared as (Personal and Shared board come later).
const (
	KindContent = "content"
	KindEntries = "entries"
)

// Notify choices for Submissions.
const (
	NotifyOff   = "off"
	NotifyEach  = "each"
	NotifyDaily = "daily"
)

// Who may save (sites.savers_mode).
const (
	SaversAnyone = "anyone"
	SaversListed = "listed"
)

// ErrOnePerPerson: this person already has a live entry in a one-per-person name.
var ErrOnePerPerson = errors.New("one entry per person")

// ErrNameFull: the name holds as many entries as it may.
var ErrNameFull = errors.New("this list is full")

// DataSettings is one data name's declaration. Kind "" = not declared.
type DataSettings struct {
	Name         string
	Kind         string
	Private      bool
	OnePerPerson bool
	Notify       string
	DeclaredAt   *time.Time
	// Legacy is the site's legacy_data flag: an undeclared name keeps the
	// behaviour from before the kinds.
	Legacy bool
}

// GetDataSettings reads one name's declaration and the site's legacy flag.
// sql.ErrNoRows when the site does not exist.
func GetDataSettings(ctx context.Context, q Querier, siteID, name string) (DataSettings, error) {
	s := DataSettings{Name: name, Notify: NotifyOff}
	var kind sql.NullString
	var declared sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT s.legacy_data, cs.kind, COALESCE(cs.private, false), COALESCE(cs.one_per_person, false),
		       COALESCE(cs.notify, 'off'), cs.declared_at
		  FROM sites s
		  LEFT JOIN collection_settings cs ON cs.site_id = s.id AND cs.collection = $2
		 WHERE s.id = $1`, siteID, name).Scan(&s.Legacy, &kind, &s.Private, &s.OnePerPerson, &s.Notify, &declared)
	if err != nil {
		return s, err
	}
	s.Kind = kind.String
	if declared.Valid {
		t := declared.Time
		s.DeclaredAt = &t
	}
	return s, nil
}

// DeclareData records name's kind and options. Declaring content always
// leaves it public (private = false). declared_at is kept from the first
// declaration.
func DeclareData(ctx context.Context, q Querier, siteID, name, kind string, private, onePerPerson bool, notify string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO collection_settings (site_id, collection, private, kind, one_per_person, notify, declared_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		ON CONFLICT (site_id, collection) DO UPDATE SET
		  private = EXCLUDED.private, kind = EXCLUDED.kind, one_per_person = EXCLUDED.one_per_person,
		  notify = EXCLUDED.notify, declared_at = COALESCE(collection_settings.declared_at, now()), updated_at = now()`,
		siteID, name, private, kind, onePerPerson, notify)
	return err
}

// CountContentNames is how many names on the site are declared Page info,
// other than except.
func CountContentNames(ctx context.Context, q Querier, siteID, except string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM collection_settings WHERE site_id = $1 AND kind = 'content' AND collection <> $2`, siteID, except).Scan(&n)
	return n, err
}

// NameHoldsItems reports whether name has more than max live items (a list
// cannot become Page info, which is one document).
func NameHoldsItems(ctx context.Context, q Querier, siteID, name string, max int) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM collection_items
		  WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL LIMIT $3) x`, siteID, name, max+1).Scan(&n)
	return n > max, err
}

// ---- Page info (content) --------------------------------------------------------

// GetContent returns the name's document (the live row), ok=false when
// nothing was saved yet.
func GetContent(ctx context.Context, q Querier, siteID, name string) (CollectionItem, bool, error) {
	var it CollectionItem
	err := q.QueryRowContext(ctx, `
		SELECT id, data, created_at FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
		 ORDER BY id DESC LIMIT 1`, siteID, name).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	return it, err == nil, err
}

// PutContent replaces the name's document with data: the earlier one goes to
// history (undo). The declaration row is locked so two first saves cannot
// make two documents. ErrSiteFull when it would grow the site past maxBytes.
func PutContent(ctx context.Context, database *sql.DB, siteID, name string, data json.RawMessage, a Actor, maxBytes int64) (CollectionItem, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&one); err != nil {
		return CollectionItem{}, err
	}
	cur, found, err := GetContent(ctx, tx, siteID, name)
	if err != nil {
		return CollectionItem{}, err
	}
	var it CollectionItem
	if found {
		err = tx.QueryRowContext(ctx, `
			UPDATE collection_items SET data = $2::jsonb, submitted_by = $3, submitted_email = $4
			 WHERE id = $1 RETURNING id, data, created_at`, cur.ID, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).
			Scan(&it.ID, &it.Data, &it.CreatedAt)
		if err != nil {
			return CollectionItem{}, err
		}
		if err := recordHistory(ctx, tx, siteID, HistoryList, name, &cur.ID, OpEdit, cur.Data, a); err != nil {
			return CollectionItem{}, err
		}
	} else {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
			VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id, data, created_at`,
			siteID, name, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
		if err != nil {
			return CollectionItem{}, err
		}
	}
	if !found || len(it.Data) > len(cur.Data) {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return CollectionItem{}, err
		}
	}
	return it, tx.Commit()
}

// ---- Submissions (entries) ------------------------------------------------------

// AppendEntry adds one entry by a signed-in person (a.ID) to a declared
// Submissions name: at most maxItems live entries in the name, and with
// onePerPerson at most one live entry per person (ErrOnePerPerson, with the
// id of the one they have). The declaration row is locked, so two saves at
// once cannot both pass either check.
func AppendEntry(ctx context.Context, database *sql.DB, siteID, name string, data json.RawMessage, a Actor, onePerPerson bool, maxItems int) (CollectionItem, int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&one); err != nil {
		return CollectionItem{}, 0, err
	}
	if onePerPerson && a.ID != "" {
		var have int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM collection_items
			 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3 AND deleted_at IS NULL
			 ORDER BY id DESC LIMIT 1`, siteID, name, a.ID).Scan(&have)
		if err == nil {
			return CollectionItem{}, have, ErrOnePerPerson
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return CollectionItem{}, 0, err
		}
	}
	full, err := NameHoldsItems(ctx, tx, siteID, name, maxItems-1)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	if full {
		return CollectionItem{}, 0, ErrNameFull
	}
	var it CollectionItem
	err = tx.QueryRowContext(ctx, `
		INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
		VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id, data, created_at`,
		siteID, name, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	return it, 0, tx.Commit()
}

// ListOwnEntries is one person's live entries in a name, newest first.
func ListOwnEntries(ctx context.Context, database *sql.DB, siteID, name, userID string, limit int, before int64) ([]CollectionItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, data, created_at FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3 AND deleted_at IS NULL
		   AND ($4 = 0 OR id < $4)
		 ORDER BY id DESC LIMIT $5`, siteID, name, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollectionItem, 0)
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// CountLiveItems is how many live items a name holds.
func CountLiveItems(ctx context.Context, q Querier, siteID, name string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL`, siteID, name).Scan(&n)
	return n, err
}

// ItemAuthor is who sent one item of a name and whether it is deleted.
// sql.ErrNoRows when the name has no such item.
type ItemAuthor struct {
	SubmittedBy string
	Email       string
	DeletedAt   *time.Time
}

// GetItemAuthor reads who sent item id of name on siteID.
func GetItemAuthor(ctx context.Context, q Querier, siteID, name string, id int64) (ItemAuthor, error) {
	var a ItemAuthor
	var by, email sql.NullString
	var del sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT submitted_by::text, COALESCE(submitted_email, CASE WHEN submitted_by IS NOT NULL THEN data->>'_submitted_by' END), deleted_at
		  FROM collection_items WHERE id = $1 AND site_id = $2 AND collection = $3`, id, siteID, name).Scan(&by, &email, &del)
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

// UndoOwnWithdrawal brings back item id of name when userID sent it and
// withdrew it themselves within the last window: the item's latest change is
// that person's own delete. ok=false otherwise (nothing changes). With
// onePerPerson it refuses (ErrOnePerPerson) when the person has another live
// entry by now.
func UndoOwnWithdrawal(ctx context.Context, database *sql.DB, siteID, name string, id int64, userID string, window time.Duration, onePerPerson bool, a Actor, maxBytes int64) (CollectionItem, bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, false, err
	}
	defer tx.Rollback()
	var op, actor sql.NullString
	var at time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT h.op, h.actor_id::text, h.created_at
		  FROM collection_items ci
		  JOIN data_history h ON h.item_id = ci.id
		 WHERE ci.id = $1 AND ci.site_id = $2 AND ci.collection = $3 AND ci.submitted_by = $4
		   AND ci.deleted_at IS NOT NULL
		 ORDER BY h.id DESC LIMIT 1
		 FOR UPDATE OF ci`, id, siteID, name, userID).Scan(&op, &actor, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return CollectionItem{}, false, nil
	}
	if err != nil {
		return CollectionItem{}, false, err
	}
	if op.String != OpDelete || actor.String != userID || time.Since(at) > window {
		return CollectionItem{}, false, nil
	}
	if onePerPerson {
		var other int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM collection_items
			 WHERE site_id = $1 AND collection = $2 AND submitted_by = $3 AND deleted_at IS NULL LIMIT 1`, siteID, name, userID).Scan(&other)
		if err == nil {
			return CollectionItem{}, false, ErrOnePerPerson
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return CollectionItem{}, false, err
		}
	}
	var it CollectionItem
	if err := tx.QueryRowContext(ctx, `
		UPDATE collection_items SET deleted_at = NULL WHERE id = $1 RETURNING id, data, created_at`, id).
		Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
		return CollectionItem{}, false, err
	}
	if err := recordHistory(ctx, tx, siteID, HistoryList, name, &id, OpUndelete, nil, a); err != nil {
		return CollectionItem{}, false, err
	}
	if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
		return CollectionItem{}, false, err
	}
	return it, true, tx.Commit()
}

// ---- who may save ---------------------------------------------------------------

// Savers is a site's who-may-save setting.
type Savers struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

// GetSavers reads a site's who-may-save setting (sorted lists).
func GetSavers(ctx context.Context, database *sql.DB, siteID string) (Savers, error) {
	s := Savers{Mode: SaversAnyone, Allow: []string{}, Block: []string{}}
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
// listed, only when the address or its domain is on the allow list. A site
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
		return mode != SaversListed, nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return mode != SaversListed, nil
	}
	keys := []string{email}
	if at := strings.LastIndex(email, "@"); at >= 0 {
		keys = append(keys, email[at:])
	}
	var blocked, allowed bool
	err = database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'block' AND pattern = ANY($2)),
		       EXISTS(SELECT 1 FROM site_savers WHERE site_id = $1 AND list = 'allow' AND pattern = ANY($2))`,
		siteID, pq.Array(keys)).Scan(&blocked, &allowed)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, nil
	}
	return mode != SaversListed || allowed, nil
}

// ---- email on new submissions ---------------------------------------------------

// NotifyDue is one name whose owner should be emailed about new entries.
type NotifyDue struct {
	SiteID     string
	SiteName   string
	Handle     string
	OwnerID    string
	Name       string
	Notify     string
	Since      time.Time
	Count      int64
	CheckUntil time.Time
}

// DueNotifications lists every name with notify each or daily whose interval
// has passed and that has new entries from people other than the owner since
// the last email (or since it was declared). Taken-down, offline and deleted
// sites are skipped.
func DueNotifications(ctx context.Context, database *sql.DB, each, daily time.Duration) ([]NotifyDue, error) {
	rows, err := database.QueryContext(ctx, `
		WITH due AS (
			SELECT cs.site_id, cs.collection, cs.notify,
			       COALESCE(cs.notify_sent_at, cs.declared_at, cs.updated_at) AS since
			  FROM collection_settings cs
			 WHERE cs.kind = 'entries' AND cs.notify IN ('each', 'daily')
			   AND COALESCE(cs.notify_sent_at, cs.declared_at, cs.updated_at) <=
			       now() - CASE WHEN cs.notify = 'each' THEN $1::interval ELSE $2::interval END
		)
		SELECT d.site_id, s.name, COALESCE(u.handle, ''), s.user_id, d.collection, d.notify, d.since, now(),
		       count(ci.id)
		  FROM due d
		  JOIN sites s ON s.id = d.site_id AND s.deleted_at IS NULL AND s.suspended_at IS NULL AND s.offline_at IS NULL
		  JOIN users u ON u.id = s.user_id
		  JOIN collection_items ci ON ci.site_id = d.site_id AND ci.collection = d.collection
		   AND ci.deleted_at IS NULL AND ci.created_at > d.since
		   AND ci.submitted_by IS DISTINCT FROM s.user_id
		 GROUP BY d.site_id, s.name, u.handle, s.user_id, d.collection, d.notify, d.since`,
		each.String(), daily.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyDue
	for rows.Next() {
		var n NotifyDue
		if err := rows.Scan(&n.SiteID, &n.SiteName, &n.Handle, &n.OwnerID, &n.Name, &n.Notify, &n.Since, &n.CheckUntil, &n.Count); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNotified records that the owner was emailed about name's entries up
// to at.
func MarkNotified(ctx context.Context, database *sql.DB, siteID, name string, at time.Time) error {
	_, err := database.ExecContext(ctx, `
		UPDATE collection_settings SET notify_sent_at = $3 WHERE site_id = $1 AND collection = $2`, siteID, name, at)
	return err
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
