package repo

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	"strings"
)

// LockAccountForDelete locks the account row inside tx. sql.ErrNoRows when
// there is no such account.
func LockAccountForDelete(ctx context.Context, tx *sql.Tx, id string) (model.AccountForDelete, error) {
	var a model.AccountForDelete
	var handle sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, username, handle, COALESCE(is_admin, false), suspended_at IS NOT NULL
		FROM users WHERE id::text = $1 FOR UPDATE`, id).Scan(&a.ID, &a.Username, &handle, &a.IsAdmin, &a.Suspended)
	if err != nil {
		return a, err
	}
	a.Handle = handle.String
	// event_domains cascades on user_id, so deleting the row would take the
	// claim with it and strand live DNS records under our domain that nothing
	// could then find or remove. The operator releases them first.
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM event_domains WHERE user_id = $1`, a.ID).Scan(&a.EventClaims)
	if err != nil {
		return a, err
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE user_id = $1 AND suspended_at IS NOT NULL`, a.ID).Scan(&a.TakenDown)
	return a, err
}

// AccountSiteKeys lists the account id and name of every site of the
// account and of the teams it belongs to, live and Recently deleted, so the
// caller can take each site's lock before erasing (a team the account alone
// belongs to goes with it).
func AccountSiteKeys(ctx context.Context, database *sql.DB, userID string) ([][2]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT user_id::text, name FROM sites
		WHERE user_id::text = $1 OR user_id IN (SELECT team_id FROM team_members WHERE user_id::text = $1)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListAccountAddresses returns every live site's own address (claimed name
// or custom domain), and the earlier one it still serves at while a new one
// is pending.
func ListAccountAddresses(ctx context.Context, database *sql.DB, userID string) ([]model.AccountAddress, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT name, lower(COALESCE(custom_domain, '')), COALESCE(domain_status, ''), lower(COALESCE(previous_domain, ''))
		  FROM sites WHERE user_id = $1 AND deleted_at IS NULL
		   AND (custom_domain IS NOT NULL OR previous_domain IS NOT NULL)
		 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AccountAddress
	for rows.Next() {
		var a model.AccountAddress
		if err := rows.Scan(&a.Site, &a.Domain, &a.Status, &a.Previous); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListRetiredNames returns the account's retired <name>.<SITE_DOMAIN> names.
func ListRetiredNames(ctx context.Context, database *sql.DB, userID string) ([]model.RetiredAccountName, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT l.hostname, COALESCE(s.name, '')
		  FROM legacy_hostnames l LEFT JOIN sites s ON s.id = l.site_id AND s.deleted_at IS NULL
		 WHERE l.user_id = $1 ORDER BY l.hostname`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RetiredAccountName
	for rows.Next() {
		var r model.RetiredAccountName
		if err := rows.Scan(&r.Name, &r.Site); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListVisitorSignIns returns the sites this person holds visitor sessions on.
func ListVisitorSignIns(ctx context.Context, database *sql.DB, userID string) ([]model.VisitorSignIn, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT site_id::text, host, min(created_at), max(last_seen_at)
		  FROM visitor_sessions WHERE user_id = $1
		 GROUP BY site_id, host ORDER BY min(created_at)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.VisitorSignIn
	for rows.Next() {
		var v model.VisitorSignIn
		if err := rows.Scan(&v.SiteID, &v.Host, &v.FirstSeen, &v.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListChangesElsewhere returns the changes this person made, signed in, to
// saved data on sites that are not theirs, oldest first.
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

type ErasureOperations struct {
	Rows            func(context.Context, *sql.Tx, string) error
	RetireSiteNames func(context.Context, *sql.Tx, string) ([]string, error)
	Before          func(context.Context, *sql.Tx, string) ([]string, error)
	After           func(context.Context, *sql.Tx, []string) ([]string, error)
}

// EraseAccount deletes the account locked by LockAccountForDelete and
// everything tied to it, inside tx: every site (Recently deleted ones too)
// with its versions, saved data, lists and analytics; keys, connected apps,
// sign-in identities and visitor sessions; the items this person submitted
// to other people's lists; pending sign-in codes sent to its email. The
// handle, old handles and claimed names are retired, not freed.
func EraseAccount(ctx context.Context, tx *sql.Tx, a model.AccountForDelete, ops ErasureOperations) (model.ErasedAccount, error) {
	out := model.ErasedAccount{UserID: a.ID, Handle: a.Handle}
	// Take what a save on these sites takes, in the order a save takes it:
	// the names' declaration rows, then their items, then the site rows
	// (the size triggers update those). A save already under way finishes
	// first; one that starts later waits for the erase. Taken in another
	// order, the erase and a visitor's save could each hold what the other
	// needs (a deadlock that aborts the erase).
	for _, q := range []string{
		`SELECT count(*) FROM (SELECT 1 FROM collection_settings WHERE site_id IN (SELECT id FROM sites WHERE user_id = $1) ORDER BY site_id, collection FOR UPDATE) x`,
		`SELECT count(*) FROM (SELECT 1 FROM collection_items WHERE site_id IN (SELECT id FROM sites WHERE user_id = $1) ORDER BY id FOR UPDATE) x`,
		`SELECT count(*) FROM (SELECT 1 FROM sites WHERE user_id = $1 ORDER BY id FOR UPDATE) x`,
	} {
		var n int64
		if err := tx.QueryRowContext(ctx, q, a.ID).Scan(&n); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM sites WHERE user_id = $1 ORDER BY name`, a.ID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s model.ErasedSite
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.Sites = append(out.Sites, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := range out.Sites {
		// Claimed <name>.<SITE_DOMAIN> addresses (current and earlier)
		// become retired names.
		domains, err := ops.RetireSiteNames(ctx, tx, out.Sites[i].ID)
		if err != nil {
			return out, err
		}
		for _, d := range domains {
			out.Sites[i].Domains = append(out.Sites[i].Domains, strings.ToLower(d))
		}
	}

	arows, err := tx.QueryContext(ctx, `SELECT handle FROM handle_aliases WHERE user_id = $1 ORDER BY handle`, a.ID)
	if err != nil {
		return out, err
	}
	for arows.Next() {
		var h string
		if err := arows.Scan(&h); err != nil {
			arows.Close()
			return out, err
		}
		out.Aliases = append(out.Aliases, h)
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return out, err
	}
	frows, err := tx.QueryContext(ctx, `SELECT suffix FROM address_families WHERE user_id = $1 ORDER BY suffix`, a.ID)
	if err != nil {
		return out, err
	}
	for frows.Next() {
		var s string
		if err := frows.Scan(&s); err != nil {
			frows.Close()
			return out, err
		}
		out.Families = append(out.Families, s)
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return out, err
	}

	// Capture product identifiers before the account cascade removes them.
	var ids []string
	if ops.Before != nil {
		ids, err = ops.Before(ctx, tx, a.ID)
		if err != nil {
			return out, err
		}
	}

	steps := []struct {
		q    string
		args []any
	}{
		// GDPR erasure: what this person submitted to anyone's list goes.
		// (Their own sites' items go with the sites.)
		{`DELETE FROM collection_items WHERE submitted_by = $1`, []any{a.ID}},
		// Their address leaves the history of other people's saved data
		// (the rows stay: they hold the site owner's earlier data).
		{`UPDATE data_history SET actor_email = NULL WHERE actor_id = $1`, []any{a.ID}},
		// Old handles and retired names stay held, by nobody.
		{`UPDATE handle_aliases SET user_id = NULL WHERE user_id = $1`, []any{a.ID}},
		{`UPDATE legacy_hostnames SET user_id = NULL WHERE user_id = $1`, []any{a.ID}},
		// Sign-in codes are keyed by email, not by account.
		{`DELETE FROM auth_tokens WHERE lower(email) = lower($1)`, []any{a.Username}},
		// Sites go first so nothing depends on the row's cascade order; the
		// row's own cascade takes keys, connected apps (grants, tokens,
		// codes), sign-in identities and visitor sessions.
		{`DELETE FROM sites WHERE user_id = $1`, []any{a.ID}},
	}
	if a.Handle != "" {
		// The handle itself joins the retired names.
		steps = append([]struct {
			q    string
			args []any
		}{{`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, NULL)
			ON CONFLICT (handle) DO UPDATE SET user_id = NULL
			WHERE handle_aliases.user_id IS NULL OR handle_aliases.user_id = $2`, []any{a.Handle, a.ID}}}, steps...)
	}
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
			return out, err
		}
	}
	if ops.Rows != nil {
		if err := ops.Rows(ctx, tx, a.ID); err != nil {
			return out, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, a.ID); err != nil {
		return out, err
	}
	// Product cleanup follows the account cascade, in the same transaction.
	if ops.After != nil {
		out.AffectedEventAccounts, err = ops.After(ctx, tx, ids)
		if err != nil {
			return out, err
		}
	}

	return out, nil
}

func InsertIssuedAccount(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	return InsertAccount(ctx, tx, name, "admin", "issued")
}

func InsertAccount(ctx context.Context, tx *sql.Tx, name, source, method string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO users (username, signup_source, signup_method) VALUES ($1, $2, $3) ON CONFLICT (username) DO NOTHING RETURNING id`, name, source, method).Scan(&id)
	return id, err
}
func LockReissueAccount(ctx context.Context, tx *sql.Tx, id string) (model.User, error) {
	var u model.User
	err := tx.QueryRowContext(ctx, `SELECT id, username, handle, is_admin, suspended_at IS NOT NULL FROM users WHERE id::text=$1 FOR UPDATE`, id).Scan(&u.ID, &u.Username, &u.Handle, &u.IsAdmin, &u.Suspended)
	return u, err
}
func LockProfile(ctx context.Context, tx *sql.Tx, id string) (sql.NullString, error) {
	var h sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT handle FROM users WHERE id=$1 FOR UPDATE", id).Scan(&h)
	return h, err
}
func AccountHasSites(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var has bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sites WHERE user_id=$1)", id).Scan(&has)
	return has, err
}
func SetUnpublishedHandle(ctx context.Context, tx *sql.Tx, id, handle string) error {
	_, err := tx.ExecContext(ctx, "UPDATE users SET handle=$2, handle_changed_at=now() WHERE id=$1", id, handle)
	return err
}
func SetDisplayName(ctx context.Context, tx *sql.Tx, id, name string) error {
	_, err := tx.ExecContext(ctx, "UPDATE users SET display_name=NULLIF($2,'') WHERE id=$1", id, name)
	return err
}

// IsUniqueConstraintViolation retains the transport's typed PostgreSQL check.
func IsUniqueConstraintViolation(err error) bool {
	var pgerr *pq.Error
	return errors.As(err, &pgerr) && pgerr.Code == "23505"
}
