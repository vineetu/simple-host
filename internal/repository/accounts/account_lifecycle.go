package accounts

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
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

// AccountSiteNames lists the names of every site of the account, live and
// Recently deleted, so the caller can take each site's lock before erasing.
func AccountSiteNames(ctx context.Context, database *sql.DB, userID string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT name FROM sites WHERE user_id::text = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
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

// ListSubmissionsElsewhere returns every list item this person submitted to
// a site that is not theirs, oldest first.
func ListSubmissionsElsewhere(ctx context.Context, database *sql.DB, userID string) ([]model.Submission, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT ci.id, ci.site_id::text, ci.collection, ci.data, ci.created_at
		  FROM collection_items ci JOIN sites s ON s.id = ci.site_id
		 WHERE ci.submitted_by = $1 AND s.user_id IS DISTINCT FROM $1
		 ORDER BY ci.created_at, ci.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Submission
	for rows.Next() {
		var s model.Submission
		if err := rows.Scan(&s.ID, &s.SiteID, &s.Collection, &s.Data, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type ErasureOperations struct {
	RetireSiteNames func(context.Context, *sql.Tx, string) ([]string, error)
	AdminTeamIDs    func(context.Context, *sql.Tx, string) ([]string, error)
	FinishTeams     func(context.Context, *sql.Tx, []string) ([]string, error)
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

	// Hack event membership (event_members.user_id REFERENCES users(id) ON
	// DELETE CASCADE) disappears with the account below, same as leaving a
	// team -- but with no handler in between to run hackteams.EnsureTeamAdmin. Read
	// which teams this person currently admins before the cascade, so a
	// successor can be chosen for each once the cascade has actually run
	// (Codex review, 2026-11-01; this account may hold no event membership
	// at all, in which case the slice is empty and the loop below is a
	// no-op).
	adminTeamIDs, err := ops.AdminTeamIDs(ctx, tx, a.ID)
	if err != nil {
		return out, err
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
		{`DELETE FROM users WHERE id = $1`, []any{a.ID}},
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
	// The cascade above already took this person's membership off each team
	// they admined. Run the same cleanup LeaveMembership runs for a
	// voluntary last-member leave, in the same order (Grok security review,
	// 2026-11-01: hackteams.EnsureTeamAdmin alone is not enough when the erased
	// account was the team's only member -- an empty, unlocked team with a
	// live code would otherwise survive the account that used to run it).
	out.AffectedEventAccounts, err = ops.FinishTeams(ctx, tx, adminTeamIDs)
	if err != nil {
		return out, err
	}

	return out, nil
}

func InsertIssuedAccount(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO users (username, signup_source, signup_method) VALUES ($1, 'admin', 'issued') ON CONFLICT (username) DO NOTHING RETURNING id`, name).Scan(&id)
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
