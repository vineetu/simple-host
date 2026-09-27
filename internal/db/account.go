package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Account deletion and export (GDPR self-service, 2026-09-27).
//
// Deleting an account is one function for both the person (DELETE /v1/me)
// and the operator (DELETE /v1/admin/users/{id}). It is immediate and final:
// nothing goes to Recently deleted. Names the account held on the platform
// domain outlive it as retired, owned by nobody, so no stranger inherits
// links that used to point at this person: claimed site names stay in
// legacy_hostnames ("this site was removed"), and the handle and every old
// handle stay in handle_aliases with no account behind them.

// AccountForDelete is the account row, locked, with what the delete policy
// needs to decide.
type AccountForDelete struct {
	ID          string
	Username    string
	Handle      string
	IsAdmin     bool
	Suspended   bool
	EventClaims int
	// TakenDown counts the account's sites the operator took down (live or
	// Recently deleted): the person cannot delete those, so neither the
	// account that holds them; the operator can.
	TakenDown int
}

// LockAccountForDelete locks the account row inside tx. sql.ErrNoRows when
// there is no such account.
func LockAccountForDelete(ctx context.Context, tx *sql.Tx, id string) (AccountForDelete, error) {
	var a AccountForDelete
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

// ErasedSite is one site removed with its account, for the disk clean-up.
type ErasedSite struct {
	ID   string
	Name string
	// Domains is every own address the site had: its custom domain or
	// claimed name and the earlier one it still served at (previous_domain),
	// to unbind and withdraw while they still point at this site.
	Domains []string
}

// ErasedAccount is what EraseAccount removed from the database and leaves
// the caller to remove on disk after the commit.
type ErasedAccount struct {
	UserID  string
	Handle  string
	Aliases []string     // earlier handles, whose disk links go too
	Sites   []ErasedSite // live and Recently deleted
}

// EraseAccount deletes the account locked by LockAccountForDelete and
// everything tied to it, inside tx: every site (Recently deleted ones too)
// with its versions, saved data, lists and analytics; keys, connected apps,
// sign-in identities and visitor sessions; the items this person submitted
// to other people's lists; pending sign-in codes sent to its email. The
// handle, old handles and claimed names are retired, not freed.
func EraseAccount(ctx context.Context, tx *sql.Tx, a AccountForDelete) (ErasedAccount, error) {
	out := ErasedAccount{UserID: a.ID, Handle: a.Handle}
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
		var s ErasedSite
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
		domains, err := RetireSiteNames(ctx, tx, out.Sites[i].ID)
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
	return out, nil
}

// ---- export ----------------------------------------------------------------

// AccountAddress is one own address of one of the account's live sites.
type AccountAddress struct {
	Site     string
	Domain   string
	Status   string
	Previous string
}

// ListAccountAddresses returns every live site's own address (claimed name
// or custom domain), and the earlier one it still serves at while a new one
// is pending.
func ListAccountAddresses(ctx context.Context, database *sql.DB, userID string) ([]AccountAddress, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT name, lower(COALESCE(custom_domain, '')), COALESCE(domain_status, ''), lower(COALESCE(previous_domain, ''))
		  FROM sites WHERE user_id = $1 AND deleted_at IS NULL
		   AND (custom_domain IS NOT NULL OR previous_domain IS NOT NULL)
		 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountAddress
	for rows.Next() {
		var a AccountAddress
		if err := rows.Scan(&a.Site, &a.Domain, &a.Status, &a.Previous); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RetiredAccountName is a name the account's sites used to have.
type RetiredAccountName struct {
	Name string
	Site string // "" when that site is gone
}

// ListRetiredNames returns the account's retired <name>.<SITE_DOMAIN> names.
func ListRetiredNames(ctx context.Context, database *sql.DB, userID string) ([]RetiredAccountName, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT l.hostname, COALESCE(s.name, '')
		  FROM legacy_hostnames l LEFT JOIN sites s ON s.id = l.site_id AND s.deleted_at IS NULL
		 WHERE l.user_id = $1 ORDER BY l.hostname`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetiredAccountName
	for rows.Next() {
		var r RetiredAccountName
		if err := rows.Scan(&r.Name, &r.Site); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListHandleAliases returns the account's earlier handles.
func ListHandleAliases(ctx context.Context, database *sql.DB, userID string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT handle FROM handle_aliases WHERE user_id = $1 ORDER BY created_at, handle`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SignInIdentity is a linked Google or GitHub sign-in.
type SignInIdentity struct {
	ID       string
	Provider string
	Email    string
	LinkedAt time.Time
}

// ListSignInIdentities returns the account's linked Google/GitHub sign-ins.
func ListSignInIdentities(ctx context.Context, database *sql.DB, userID string) ([]SignInIdentity, error) {
	rows, err := database.QueryContext(ctx, `SELECT id::text, provider, COALESCE(email, ''), created_at FROM oauth_identities WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SignInIdentity
	for rows.Next() {
		var s SignInIdentity
		if err := rows.Scan(&s.ID, &s.Provider, &s.Email, &s.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UnlinkSignInIdentity removes one of the account's linked Google/GitHub
// sign-ins: that Google or GitHub account no longer signs in here.
// sql.ErrNoRows when the account has no such link.
// Every site sign-in of the account ends with it (EndVisitorSessions): a
// session cannot say which sign-in made it, and removing one is how a person
// shuts out whoever used it.
func UnlinkSignInIdentity(ctx context.Context, database *sql.DB, userID, id string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM oauth_identities WHERE user_id = $1 AND id::text = $2`, userID, id)
	if err := oneRow(res, err); err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// VisitorSignIn is a site this person is signed in to as a visitor.
type VisitorSignIn struct {
	SiteID    string
	Host      string
	FirstSeen time.Time
	LastSeen  time.Time
}

// ListVisitorSignIns returns the sites this person holds visitor sessions on.
func ListVisitorSignIns(ctx context.Context, database *sql.DB, userID string) ([]VisitorSignIn, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT site_id::text, host, min(created_at), max(last_seen_at)
		  FROM visitor_sessions WHERE user_id = $1
		 GROUP BY site_id, host ORDER BY min(created_at)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VisitorSignIn
	for rows.Next() {
		var v VisitorSignIn
		if err := rows.Scan(&v.SiteID, &v.Host, &v.FirstSeen, &v.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Submission is one list item this person submitted to someone else's site.
type Submission struct {
	ID         int64
	SiteID     string
	Collection string
	Data       []byte
	CreatedAt  time.Time
}

// ListSubmissionsElsewhere returns every list item this person submitted to
// a site that is not theirs, oldest first.
func ListSubmissionsElsewhere(ctx context.Context, database *sql.DB, userID string) ([]Submission, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT ci.id, ci.site_id::text, ci.collection, ci.data, ci.created_at
		  FROM collection_items ci JOIN sites s ON s.id = ci.site_id
		 WHERE ci.submitted_by = $1 AND s.user_id IS DISTINCT FROM $1
		 ORDER BY ci.created_at, ci.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Submission
	for rows.Next() {
		var s Submission
		if err := rows.Scan(&s.ID, &s.SiteID, &s.Collection, &s.Data, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ExportItem is one list item as the export writes it.
type ExportItem struct {
	ID          int64
	Collection  string
	Data        []byte
	CreatedAt   time.Time
	SubmittedBy string // the identity the visitor sent it as, "" for public lists
}

// ListExportItems returns every item of every list of a site (other people's
// Personal records left out), with its id,
// time and who submitted it (items sent while signed in). That is the address
// the visitor signed in with on the site, kept with the item,
// never a join to their account: the account's email may be one they did
// not give the site owner, and it changes later.
func ListExportItems(ctx context.Context, database *sql.DB, siteID string) ([]ExportItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT ci.id, ci.collection, ci.data, ci.created_at,
		       CASE WHEN ci.submitted_by IS NOT NULL THEN COALESCE(ci.submitted_email, ci.data->>'_submitted_by', '') ELSE '' END
		  FROM collection_items ci
		 WHERE ci.site_id = $1 AND ci.deleted_at IS NULL
		   -- Personal records are their people's own: the site's export
		   -- carries only the owner's own record, never anyone else's.
		   AND NOT EXISTS (
		       SELECT 1 FROM collection_settings cs JOIN sites s ON s.id = cs.site_id
		        WHERE cs.site_id = ci.site_id AND cs.collection = ci.collection AND cs.kind = 'mine'
		          AND ci.submitted_by IS DISTINCT FROM s.user_id)
		 ORDER BY ci.collection, ci.id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportItem
	for rows.Next() {
		var it ExportItem
		var created sql.NullTime
		if err := rows.Scan(&it.ID, &it.Collection, &it.Data, &created, &it.SubmittedBy); err != nil {
			return nil, err
		}
		it.CreatedAt = created.Time
		out = append(out, it)
	}
	return out, rows.Err()
}
