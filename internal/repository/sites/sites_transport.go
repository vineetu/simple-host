package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

func BackfillPreviewExpiry(ctx context.Context, db *sql.DB, username string, ttlHours int) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE sites SET expires_at = created_at + make_interval(hours => $1)
		 WHERE expires_at IS NULL
		   AND user_id IN (SELECT id FROM users WHERE lower(username) = lower($2))`,
		ttlHours, username)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func GetAllowedOriginsByID(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `SELECT allowed_origins FROM sites WHERE id = $1`, siteID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	var out []string
	for _, o := range strings.Split(raw.String, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out, nil
}

func GetSiteStateVersionByID(ctx context.Context, db *sql.DB, siteID string) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `SELECT state_version FROM sites WHERE id = $1`, siteID).Scan(&version)
	return version, err
}

func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(u.handle, ''), u.id::text, s.name
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.id::text = $1 AND s.deleted_at IS NULL`, siteID).Scan(&handle, &userID, &name)
	return
}

func HomeSite(ctx context.Context, q Querier, userID string) (model.Site, bool, error) {
	var id sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT home_site_id FROM users WHERE id = $1`, userID).Scan(&id); err != nil {
		return model.Site{}, false, err
	}
	if !id.Valid {
		return model.Site{}, false, nil
	}
	s, err := GetSiteByID(ctx, q, id.String)
	if err == sql.ErrNoRows || (err == nil && s.Deleted) {
		_, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1 AND home_site_id = $2`, userID, id.String)
		return model.Site{}, false, err
	}
	return s, err == nil, err
}

func SetHomeSite(ctx context.Context, q Querier, userID string, name *string) error {
	var res sql.Result
	var err error
	if name == nil {
		res, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1`, userID)
	} else {
		res, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = s.id FROM sites s WHERE users.id = $1 AND s.user_id = users.id AND s.name = $2 AND s.deleted_at IS NULL`, userID, *name)
	}
	return oneRow(res, err)
}

func ShowcasePreferences(ctx context.Context, q *sql.DB, userID string) (map[string]model.ShowcasePreference, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,showcase_pinned,showcase_order FROM sites WHERE deleted_at IS NULL AND ($1='' OR user_id::text=$1)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ShowcasePreference{}
	for rows.Next() {
		var id string
		var p model.ShowcasePreference
		if err := rows.Scan(&id, &p.Pinned, &p.Order); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

func GetShowcasePreference(ctx context.Context, q Querier, userID, name string) (model.ShowcasePreference, error) {
	var p model.ShowcasePreference
	err := q.QueryRowContext(ctx, `SELECT showcase_pinned,showcase_order FROM sites WHERE user_id=$1 AND name=$2 AND deleted_at IS NULL`, userID, name).Scan(&p.Pinned, &p.Order)
	return p, err
}

func SetShowcasePreference(ctx context.Context, q Querier, userID, name string, pinned *bool, order *int) (model.ShowcasePreference, error) {
	var p model.ShowcasePreference
	err := q.QueryRowContext(ctx, `UPDATE sites SET showcase_pinned=COALESCE($3,showcase_pinned),showcase_order=COALESCE($4,showcase_order),updated_at=now() WHERE user_id=$1 AND name=$2 AND deleted_at IS NULL RETURNING showcase_pinned,showcase_order`, userID, name, pinned, order).Scan(&p.Pinned, &p.Order)
	return p, err
}

func ResolveOldSiteName(ctx context.Context, q Querier, userID, name string) (siteID, current string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT s.id, s.name
		  FROM site_name_aliases a JOIN sites s ON s.id = a.site_id
		 WHERE a.user_id::text = $1 AND a.name = $2
		   AND s.user_id = a.user_id AND s.deleted_at IS NULL`, userID, name).Scan(&siteID, &current)
	return siteID, current, err
}

func ListHandlesWithSites(ctx context.Context, q *sql.DB) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT u.handle FROM users u JOIN sites s ON s.user_id = u.id AND s.deleted_at IS NULL
		WHERE u.handle IS NOT NULL AND u.handle <> ''
		ORDER BY u.handle`)
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

func SetAllowedOrigins(ctx context.Context, db *sql.DB, siteID, origins string) error {
	_, err := db.ExecContext(ctx, `UPDATE sites SET allowed_origins = $1 WHERE id = $2`, origins, siteID)
	return err
}

func SetAllowAnonymousWrites(ctx context.Context, q Querier, siteID string, allow bool) error {
	_, err := q.ExecContext(ctx,
		`UPDATE sites SET allow_anonymous_writes = $1 WHERE id = $2`,
		allow, siteID)
	return err
}

func SetSiteOffline(ctx context.Context, q Querier, siteID string, on bool) error {
	query := `UPDATE sites SET offline_at = NULL, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`
	if on {
		query = `UPDATE sites SET offline_at = COALESCE(offline_at, now()), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`
	}
	res, err := q.ExecContext(ctx, query, siteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ListSiteIdleFlags(ctx context.Context, database *sql.DB, userID string) (map[string]model.SiteIdleFlags, error) {
	q := `SELECT id, idle_keep, idle_warned_at, idle_remove_at FROM sites WHERE deleted_at IS NULL AND (idle_keep OR idle_warned_at IS NOT NULL)`
	args := []any{}
	if userID != "" {
		q += ` AND user_id = $1`
		args = append(args, userID)
	}
	rows, err := database.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.SiteIdleFlags{}
	for rows.Next() {
		var id string
		var f model.SiteIdleFlags
		if err := rows.Scan(&id, &f.Keep, &f.WarnedAt, &f.RemoveAt); err != nil {
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}

func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, f [2]string) (model.SiteDomainInfo, error) {
	// A platform name is found under whichever platform domain it is stored.
	info, err := ScanDomainInfo(database.QueryRowContext(ctx, `SELECT `+DomainColumns+` FROM sites WHERE custom_domain IN ($1, $2) AND deleted_at IS NULL ORDER BY custom_domain = $1 DESC LIMIT 1`, f[0], f[1]))
	if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	info, err = ScanDomainInfo(database.QueryRowContext(ctx, `SELECT `+DomainColumns+` FROM sites WHERE previous_domain IN ($1, $2) AND deleted_at IS NULL ORDER BY previous_domain = $1 DESC LIMIT 1`, f[0], f[1]))
	if err != nil {
		return model.SiteDomainInfo{}, err
	}
	return info.AsPrevious(time.Now()), nil
}

// LockDomainCertAccount serializes certificate budget decisions for an account.
func LockDomainCertAccount(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('domain-cert:' || $1, 0))`, userID)
	return err
}

func DomainCertRequestCounts(ctx context.Context, tx *sql.Tx, userID, domain string) (again bool, n int, err error) {
	err = tx.QueryRowContext(ctx, `
        SELECT COALESCE(bool_or(domain = $2), false), count(DISTINCT domain)
        FROM domain_cert_requests WHERE user_id = $1 AND requested_at > now() - interval '24 hours'`, userID, domain).Scan(&again, &n)
	return
}

func RecordDomainCertRequest(ctx context.Context, tx *sql.Tx, userID, domain string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO domain_cert_requests (user_id, domain) VALUES ($1, $2)`, userID, domain)
	return err
}
