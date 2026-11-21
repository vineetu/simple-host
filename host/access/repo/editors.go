package repo

import (
	"context"
	"database/sql"

	"strings"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/model"
	siterules "github.com/vsriram/simple-host/host/rules/sites"
)

// Site editors (0027-site-editors.sql): a person who may act as the owner of
// one site of an account, named by the site's name.

// GetAccountByHandle loads the account, team or person, whose handle is
// handle (sql.ErrNoRows when none has it). A value no handle could have is
// sql.ErrNoRows too, without a query.
func GetAccountByHandle(ctx context.Context, q Querier, handle string) (model.User, error) {
	return accountByHandle(ctx, q, handle, "")
}

// accountByHandle loads the users row whose handle is handle; and is SQL
// appended to the WHERE clause (" AND is_team" for a team only).
func accountByHandle(ctx context.Context, q Querier, handle, and string) (model.User, error) {
	var u model.User
	handle = strings.ToLower(strings.TrimSpace(handle))
	if !siterules.VisitorHandle.MatchString(handle) {
		return u, sql.ErrNoRows
	}
	err := q.QueryRowContext(ctx, `
		SELECT id, username, created_at, handle, display_name, suspended_at IS NOT NULL, COALESCE(suspended_reason, '')
		FROM users WHERE handle = $1`+and, handle).Scan(
		&u.ID, &u.Username, &u.CreatedAt, &u.Handle, &u.DisplayName, &u.Suspended, &u.SuspendedReason)
	return u, err
}

// ActsAsOwnerOf reports whether personID belongs to the team accountID or,
// when siteName is set, is an editor of that site of it.
func ActsAsOwnerOf(ctx context.Context, q Querier, accountID, personID, siteName string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id::text = $1 AND user_id::text = $2)
		    OR ($3 <> '' AND EXISTS (SELECT 1 FROM site_editors WHERE account_id::text = $1 AND user_id::text = $2 AND site_name = $3))`,
		accountID, personID, strings.ToLower(siteName)).Scan(&ok)
	return ok, err
}

// SetSiteEditor makes personID an editor of the account's site siteName, or
// stops them being one; false when nothing changed.
func SetSiteEditor(ctx context.Context, q Querier, accountID, siteName, personID, addedBy string, on bool) (bool, error) {
	query := `INSERT INTO site_editors (account_id, site_name, user_id, added_by) VALUES ($1, $2, $3, NULLIF($4, '')::uuid) ON CONFLICT DO NOTHING`
	args := []any{accountID, strings.ToLower(siteName), personID, addedBy}
	if !on {
		query, args = `DELETE FROM site_editors WHERE account_id = $1 AND site_name = $2 AND user_id = $3`, args[:3]
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListSiteEditors lists the editors of the account's site siteName, earliest first.
func ListSiteEditors(ctx context.Context, q Querier, accountID, siteName string) ([]model.TeamMember, error) {
	return scanTeamMembers(q.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.handle, ''), COALESCE(u.display_name, ''), COALESCE(a.handle, ''), e.added_at
		FROM site_editors e
		JOIN users u ON u.id = e.user_id
		LEFT JOIN users a ON a.id = e.added_by
		WHERE e.account_id = $1 AND e.site_name = $2
		ORDER BY e.added_at, u.handle`, accountID, strings.ToLower(siteName)))
}

// ReplaceSiteEditors makes the editors of the account's sites exactly
// editors (site name -> people): a product keeping its groups in step.
func ReplaceSiteEditors(ctx context.Context, q Querier, accountID string, editors map[string][]string) error {
	var names, people []string
	for name, persons := range editors {
		for _, p := range persons {
			names, people = append(names, strings.ToLower(name)), append(people, p)
		}
	}
	if _, err := q.ExecContext(ctx, `
		DELETE FROM site_editors e WHERE e.account_id = $1 AND NOT EXISTS (
			SELECT 1 FROM unnest($2::text[], $3::text[]) w(site_name, user_id)
			WHERE w.site_name = e.site_name AND w.user_id = e.user_id::text)`, accountID, pq.Array(names), pq.Array(people)); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO site_editors (account_id, site_name, user_id)
		SELECT $1, w.site_name, w.user_id::uuid FROM unnest($2::text[], $3::text[]) w(site_name, user_id)
		ON CONFLICT DO NOTHING`, accountID, pq.Array(names), pq.Array(people))
	return err
}

// EditedSites lists the sites of the account that personID edits.
func EditedSites(ctx context.Context, q Querier, accountID, personID string) ([]string, error) {
	return scanStrings(ctx, q, `SELECT site_name FROM site_editors WHERE account_id::text = $1 AND user_id::text = $2 ORDER BY site_name`, accountID, personID)
}

// MoveSiteEditors keeps a site's editors when it is renamed (to "" drops
// them: the site is deleted for good).
func MoveSiteEditors(ctx context.Context, q Querier, accountID, from, to string) error {
	if to == "" {
		_, err := q.ExecContext(ctx, `DELETE FROM site_editors WHERE account_id = $1 AND site_name = $2`, accountID, from)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE site_editors SET site_name = $3 WHERE account_id = $1 AND site_name = $2`, accountID, from, to)
	return err
}
