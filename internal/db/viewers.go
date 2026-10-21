package db

import (
	"context"
	"database/sql"
	"time"
)

// Named viewers (handler/viewers.go): sites.access is 'anyone' or 'specific';
// at 'specific' only the owner and the emails in site_viewers can open the
// site.

// SiteViewer is one named viewer of a site. SignedInAt is nil until that
// email has signed in to this site at least once (site_visitor_signins,
// matched through the account the email signs in as: an email-code account's
// username is that exact email, an OAuth account's is its oauth_identities
// row).
type SiteViewer struct {
	Email      string     `json:"email"`
	AddedAt    time.Time  `json:"added_at"`
	SignedInAt *time.Time `json:"signed_in_at,omitempty"`
}

// SetSiteAccess sets sites.access ('anyone' or 'specific') of a live site.
// sql.ErrNoRows for an unknown or deleted site.
func SetSiteAccess(ctx context.Context, q Querier, siteID, access string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET access = $2 WHERE id = $1 AND deleted_at IS NULL`, siteID, access)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListSiteViewers returns a site's named viewers, oldest first, each with
// when they first signed in to this site (nil if never).
func ListSiteViewers(ctx context.Context, q *sql.DB, siteID string) ([]SiteViewer, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT v.email, v.added_at,
		  (SELECT MIN(s.first_at) FROM site_visitor_signins s WHERE s.site_id = v.site_id AND s.user_id IN (
		    SELECT u.id FROM users u WHERE lower(u.username) = v.email
		    UNION
		    SELECT oi.user_id FROM oauth_identities oi WHERE lower(oi.email) = v.email
		  ))
		FROM site_viewers v WHERE v.site_id = $1 ORDER BY v.added_at, v.email`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteViewer{}
	for rows.Next() {
		var v SiteViewer
		var signedIn sql.NullTime
		if err := rows.Scan(&v.Email, &v.AddedAt, &signedIn); err != nil {
			return nil, err
		}
		if signedIn.Valid {
			t := signedIn.Time
			v.SignedInAt = &t
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountSiteViewers is how many named viewers a site has.
func CountSiteViewers(ctx context.Context, q Querier, siteID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM site_viewers WHERE site_id = $1`, siteID).Scan(&n)
	return n, err
}

// AddSiteViewer names one viewer (an already normalised email). Reports
// whether it was new.
func AddSiteViewer(ctx context.Context, q Querier, siteID, email string) (bool, error) {
	res, err := q.ExecContext(ctx, `INSERT INTO site_viewers (site_id, email) VALUES ($1, $2) ON CONFLICT DO NOTHING`, siteID, email)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RemoveSiteViewer removes one named viewer. Reports whether it was there.
func RemoveSiteViewer(ctx context.Context, q Querier, siteID, email string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM site_viewers WHERE site_id = $1 AND email = $2`, siteID, email)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// IsSiteViewer reports whether email (already normalised) is a named viewer
// of the site.
func IsSiteViewer(ctx context.Context, q Querier, siteID, email string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM site_viewers WHERE site_id = $1 AND email = $2)`, siteID, email).Scan(&ok)
	return ok, err
}
