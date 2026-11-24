package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// copiedSites are the old sites that move: live ones, and those in the bin
// still inside their 7 days. Expired preview sites and orphans stay behind.
const copiedSites = `(SELECT id FROM sites WHERE user_id IS NOT NULL AND (deleted_at IS NULL OR deleted_at > now() - interval '7 days')
	AND (expires_at IS NULL OR expires_at > now()))`

// siteDir is a site's folder under the old data folder.
const siteDir = `(CASE WHEN s.deleted_at IS NULL THEN 'by-id/' || s.user_id || '/' || s.name ELSE 'deleted/' || s.user_id || '/' || s.id END)`

// isShort: a custom domain that is one label under the app domain, a free short name.
const isShort = `lower(s.custom_domain) = split_part(lower(s.custom_domain), '.', 1) || '.' || {app}`

// nameCandidates are every name the old service holds, best claim first:
// handles, live short names, old handles, released short names.
const nameCandidates = `SELECT handle AS name, 'handle' AS kind, id AS person_id, NULL::uuid AS site_id, COALESCE(handle_changed_at, created_at, {never}) AS created_at, NULL::timestamptz AS retired_at, 1 AS pri FROM users WHERE handle IS NOT NULL
	UNION ALL SELECT split_part(lower(s.custom_domain), '.', 1), 'short', s.user_id, s.id, COALESCE(s.domain_bound_at, s.created_at, {never}), NULL, 2 FROM sites s
		WHERE s.id IN {copied} AND s.domain_verified_at IS NOT NULL AND ` + isShort + `
	UNION ALL SELECT handle, 'handle', user_id, NULL, created_at, created_at, 3 FROM handle_aliases
	UNION ALL SELECT split_part(lower(hostname), '.', 1), 'short', NULL, NULL, COALESCE(created_at, {never}), COALESCE(created_at, {never}), 4 FROM legacy_hostnames
		WHERE lower(hostname) = split_part(lower(hostname), '.', 1) || '.' || {app}`

// recentVisits: the site had people visiting in the last 90 days.
const recentVisits = `(EXISTS (SELECT 1 FROM site_view_hourly h WHERE h.site_id = s.id AND h.class = 'person' AND h.hour > now() - interval '90 days')
	OR EXISTS (SELECT 1 FROM site_view_daily d WHERE d.site_id = s.id AND d.day > now() - interval '90 days'))`

// table is one new table: its columns (the first keys of them are the key)
// and the old-side query giving them, or a Go function for tables whose
// rows come with files.
type table struct {
	name string
	keys int
	cols []string
	old  string
	rows func(r *runner, ctx context.Context, write bool) ([]row, error)
}

type row = []sql.NullString

// tables in the order they are written (removals run in reverse).
var tables = []table{
	{"people", 1, cols("id email display_name is_team operator created_at suspended_at suspended_reason"), `
		SELECT id, CASE WHEN is_team THEN NULL ELSE lower(username) END, COALESCE(display_name, ''), is_team, COALESCE(is_admin, false),
			COALESCE(created_at, {never}), suspended_at, COALESCE(suspended_reason, '') FROM users
		UNION ALL SELECT md5('viewer:' || e)::uuid, e, '', false, false, min(added_at), NULL, ''
			FROM (SELECT lower(email) AS e, added_at FROM site_viewers WHERE site_id IN {copied}) v
			WHERE e NOT IN (SELECT lower(username) FROM users WHERE NOT is_team) GROUP BY e`, nil},
	{"sites", 1, cols("id owner_id name created_at changed_at live_version listed offline deleted_at taken_down_at taken_down_reason access passcode_enc passcode_gen"), `
		SELECT s.id, s.user_id, s.name, COALESCE(s.created_at, {never}), COALESCE(s.updated_at, s.created_at, {never}),
			CASE WHEN EXISTS (SELECT 1 FROM versions v WHERE v.site_id = s.id AND v.version_number = s.active_version AND v.status = 'active') THEN s.active_version ELSE 0 END,
			s.visibility = 'public', s.offline_at IS NOT NULL, s.deleted_at, s.suspended_at, COALESCE(s.suspended_reason, ''),
			CASE WHEN s.passcode_enc IS NOT NULL THEN 'passcode' WHEN s.access = 'specific' THEN 'viewers' ELSE 'open' END,
			s.passcode_enc, s.passcode_generation FROM sites s WHERE s.id IN {copied}`, nil},
	{"names", 1, cols("name kind person_id site_id created_at retired_at"),
		`SELECT DISTINCT ON (name) name, kind, person_id, site_id, created_at, retired_at FROM (` + nameCandidates + `) c ORDER BY name, pri`, nil},
	{"owner_addresses", 1, cols("owner_id home_site_id app_shape"), "", (*runner).addresses},
	{"keys", 1, cols("id hash person_id name last4 created_at last_used_at"), `
		SELECT id, key_hash, user_id, COALESCE(name, ''), COALESCE(last4, ''), created_at, last_used_at FROM api_keys
		WHERE COALESCE(name, '') <> 'dashboard sign-in' AND (expires_at IS NULL OR expires_at > now())`, nil},
	{"team_members", 2, cols("team_id person_id joined_at"), `SELECT team_id, user_id, added_at FROM team_members`, nil},
	{"team_invites", 1, cols("id team_id email invited_by created_at accepted_at"), `
		SELECT id, team_id, addressed_to, invited_by, created_at, NULL::timestamptz FROM team_invitations WHERE state = 'pending' AND expires_at > now()`, nil},
	{"oauth_clients", 1, cols("id name redirect_uris secret_hash metadata created_at"), `
		SELECT client_id, client_name, redirect_uris, client_secret_hash, jsonb_build_object('token_endpoint_auth_method', token_endpoint_auth_method,
			'pkce_required', pkce_required, 'dynamic', dynamic), created_at FROM oauth_clients`, nil},
	{"oauth_grants", 1, cols("id person_id client_id scope created_at"), `
		SELECT DISTINCT ON (user_id, client_id) id, user_id, client_id, scope, created_at FROM oauth_grants ORDER BY user_id, client_id, last_used_at DESC, id`, nil},
	{"oauth_tokens", 1, cols("hash kind grant_id person_id created_at expires_at"), `
		WITH kept AS (SELECT DISTINCT ON (user_id, client_id) id, user_id, client_id FROM oauth_grants ORDER BY user_id, client_id, last_used_at DESC, id)
		SELECT t.token_hash, t.kind, k.id, g.user_id, t.created_at, t.expires_at FROM oauth_tokens t
		JOIN oauth_grants g ON g.id = t.grant_id JOIN kept k ON k.user_id = g.user_id AND k.client_id = g.client_id
		WHERE t.expires_at > now() AND t.used_at IS NULL`, nil},
	{"visitor_sessions", 1, cols("id person_id site_id host created_at last_seen_at expires_at"), `
		SELECT sha256(id), user_id, site_id, host, created_at, last_seen_at, LEAST(expires_at, idle_expires_at) FROM visitor_sessions
		WHERE site_id IN {copied} AND expires_at > now() AND idle_expires_at > now()`, nil},
	{"visitor_signins", 2, cols("site_id person_id first_at confirmed"), `
		SELECT site_id, user_id, min(at), bool_or(confirmed) FROM (
			SELECT site_id, user_id, first_at AS at, false AS confirmed FROM site_visitor_signins
			UNION ALL SELECT site_id, user_id, created_at, true FROM sso_consents) x WHERE site_id IN {copied} GROUP BY 1, 2`, nil},
	{"viewers", 2, cols("site_id person_id added_at"), `
		SELECT v.site_id, COALESCE(u.id, md5('viewer:' || lower(v.email))::uuid), min(v.added_at) FROM site_viewers v
		LEFT JOIN users u ON lower(u.username) = lower(v.email) AND NOT u.is_team WHERE v.site_id IN {copied} GROUP BY 1, 2`, nil},
	{"versions", 2, cols("site_id n author_id created_at bytes"), "", (*runner).versions},
	{"hostnames", 1, cols("host kind site_id owner_id prefix proof_token proven_at status cert_status cert_expires_at error created_at"), "", (*runner).hostnames},
	{"namespaces", 2, cols("site_id name rules spam_check created_at"), "", (*runner).namespaces},
	{"push_devices", 1, cols("id person_id endpoint p256dh auth created_at"), `
		SELECT DISTINCT ON (owner_id, url) md5('push:' || owner_id || url)::uuid, owner_id, url, push_p256dh, push_auth, created_at FROM site_notifications
		WHERE kind = 'web-push' AND state = 'active' AND subscriber_role = 'owner' AND site_id IN {copied} ORDER BY owner_id, url, created_at`, nil},
	{"stats_daily", 2, cols("site_id day views people bots"), `
		SELECT site_id, day, sum(views), sum(people), sum(bots) FROM (
			SELECT site_id, (hour AT TIME ZONE 'UTC')::date AS day, CASE WHEN class = 'person' THEN views ELSE 0 END AS views, 0 AS people,
				CASE WHEN class = 'bot' THEN views ELSE 0 END AS bots FROM site_view_hourly
			UNION ALL SELECT site_id, (hour AT TIME ZONE 'UTC')::date, 0, count(DISTINCT ip_hash), 0 FROM site_visitor_hourly WHERE class = 'person' GROUP BY 1, 2
			UNION ALL SELECT site_id, day, views, 0, 0 FROM site_view_daily d
				WHERE NOT EXISTS (SELECT 1 FROM site_view_hourly h WHERE h.site_id = d.site_id AND (h.hour AT TIME ZONE 'UTC')::date = d.day)) x
		WHERE site_id IN {copied} GROUP BY 1, 2`, nil},
	{"stats_keys", 4, cols("site_id day kind key n"), `
		SELECT site_id, day, 'page', path, views FROM site_page_daily WHERE site_id IN {copied}
		UNION ALL SELECT site_id, day, 'referrer', domain, views FROM site_referrer_daily WHERE site_id IN {copied}
		UNION ALL SELECT site_id, day, 'country', country, views FROM site_geo_daily WHERE class = 'person' AND site_id IN {copied}`, nil},
	{"api_daily", 2, cols("day country calls people"), `SELECT day, key, calls, 0 FROM api_growth_daily WHERE dim = 'country'`, nil},
	{"net_usage_daily", 1, cols("day bytes_in bytes_out"), `SELECT day, sum(rx_bytes), sum(tx_bytes) FROM net_usage_daily GROUP BY day`, nil},
}

func cols(s string) []string { return strings.Fields(s) }

// texts selects every column as its text form, so both sides compare as text
// and every value goes back in as text.
func texts(cs []string) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = `"` + c + `"::text`
	}
	return strings.Join(out, ", ")
}

func query(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, sqlText string, args ...any) ([]row, error) {
	rows, err := q.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("%w in: %.80s", err, strings.Join(strings.Fields(sqlText), " "))
	}
	defer rows.Close()
	n, _ := rows.Columns()
	var out []row
	for rows.Next() {
		x := make(row, len(n))
		ptrs := make([]any, len(x))
		for i := range x {
			ptrs[i] = &x[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// want is what the old service says the table should hold.
func (r *runner) want(ctx context.Context, t table, write bool) ([]row, error) {
	if t.rows != nil {
		return t.rows(r, ctx, write)
	}
	q := fmt.Sprintf(`SELECT %s FROM (%s) x(%s)`, texts(t.cols), r.sql(t.old), `"`+strings.Join(t.cols, `", "`)+`"`)
	return query(ctx, r.old, q)
}

func key(x row, n int) string {
	var b strings.Builder
	for _, v := range x[:n] {
		if v.Valid {
			b.WriteString(v.String)
		} else {
			b.WriteString("\x01")
		}
		b.WriteByte(0)
	}
	return b.String()
}

// copy writes every table: removals first (in reverse order, so nothing
// still points at a removed row and unique values are free), then upserts
// that change only rows that differ. Folders the copy no longer wants go last.
func (r *runner) copy(ctx context.Context) error {
	want := make([][]row, len(tables))
	for i, t := range tables {
		var err error
		if want[i], err = r.want(ctx, t, true); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
	}
	tx, err := r.new.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := len(tables) - 1; i >= 0; i-- {
		t, wanted := tables[i], map[string]bool{}
		for _, x := range want[i] {
			wanted[key(x, t.keys)] = true
		}
		have, err := query(ctx, tx, `SELECT `+texts(t.cols[:t.keys])+` FROM `+t.name)
		if err != nil {
			return err
		}
		where := make([]string, t.keys)
		for j, c := range t.cols[:t.keys] {
			where[j] = fmt.Sprintf(`"%s"::text = $%d`, c, j+1)
		}
		for _, x := range have {
			if !wanted[key(x, t.keys)] {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+t.name+` WHERE `+strings.Join(where, " AND "), args(x)...); err != nil {
					return fmt.Errorf("%s: %w", t.name, err)
				}
				r.changes++
			}
		}
	}
	for i, t := range tables {
		types := map[string]string{}
		typed, err := query(ctx, tx, `SELECT attname, format_type(atttypid, atttypmod) FROM pg_attribute WHERE attrelid = $1::regclass AND attnum > 0`, t.name)
		for _, x := range typed {
			types[x[0].String] = x[1].String
		}
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, upsert(t, types))
		if err != nil {
			return err
		}
		for _, x := range want[i] {
			res, err := stmt.ExecContext(ctx, args(x)...)
			if err != nil {
				return fmt.Errorf("%s %q: %w", t.name, key(x, t.keys), err)
			}
			n, _ := res.RowsAffected()
			r.changes += int(n)
		}
		stmt.Close()
	}
	// The idle clock of every site starts on the day it is copied.
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET last_visit_at = now() WHERE last_visit_at IS NULL`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.prune()
}

// upsert writes one row given as text; each value is cast to its column's
// type, so bytea, arrays and JSON go in exactly as they read out.
func upsert(t table, types map[string]string) string {
	ph, set, mine, theirs := make([]string, len(t.cols)), []string{}, []string{}, []string{}
	for i, c := range t.cols {
		ph[i] = fmt.Sprintf("$%d::text::%s", i+1, types[c])
		if i >= t.keys {
			set = append(set, fmt.Sprintf(`"%s" = EXCLUDED."%s"`, c, c))
			mine, theirs = append(mine, t.name+`."`+c+`"`), append(theirs, `EXCLUDED."`+c+`"`)
		}
	}
	q := fmt.Sprintf(`INSERT INTO %s ("%s") VALUES (%s) ON CONFLICT ("%s") `, t.name, strings.Join(t.cols, `", "`), strings.Join(ph, ", "),
		strings.Join(t.cols[:t.keys], `", "`))
	return q + fmt.Sprintf(`DO UPDATE SET %s WHERE (%s) IS DISTINCT FROM (%s)`, strings.Join(set, ", "), strings.Join(mine, ", "), strings.Join(theirs, ", "))
}

func args(x row) []any {
	out := make([]any, len(x))
	for i, v := range x {
		out[i] = v
	}
	return out
}

// compare reads every new table and lists how it differs from what the old
// service says it should hold. Secrets are never printed: only the column.
func (r *runner) compare(ctx context.Context) error {
	for _, t := range tables {
		want, err := r.want(ctx, t, false)
		if err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		have, err := query(ctx, r.new, `SELECT `+texts(t.cols)+` FROM `+t.name)
		if err != nil {
			return err
		}
		got := map[string]row{}
		for _, x := range have {
			got[key(x, t.keys)] = x
		}
		for _, x := range want {
			k := key(x, t.keys)
			y, ok := got[k]
			delete(got, k)
			if !ok {
				r.diff("%s %s: missing in the new database", t.name, show(x, t.keys))
				continue
			}
			for i := t.keys; i < len(t.cols); i++ {
				if x[i] != y[i] {
					r.diff("%s %s: %s differs", t.name, show(x, t.keys), t.cols[i])
				}
			}
		}
		for _, y := range got {
			r.diff("%s %s: in the new database but not in the old", t.name, show(y, t.keys))
		}
	}
	return nil
}

// show is a row's key for a difference line. Hashed keys are shortened.
func show(x row, n int) string {
	parts := make([]string, n)
	for i, v := range x[:n] {
		parts[i] = v.String
		if len(parts[i]) > 40 {
			parts[i] = parts[i][:12] + "…"
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}
