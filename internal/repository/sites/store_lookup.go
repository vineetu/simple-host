package sites

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

func DomainHeldByDeletedSite(ctx context.Context, q Querier, host string, f [2]string) (bool, error) {
	var held bool

	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE deleted_at IS NOT NULL
		AND (custom_domain IN (lower($1), lower($2)) OR previous_domain IN (lower($1), lower($2))))`, f[0], f[1]).Scan(&held)
	return held, err
}
func GetHandleBySiteName(ctx context.Context, db *sql.DB, name string) (string, error) {
	const query = `
		SELECT COALESCE(u.handle, '')
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.name = $1 AND s.deleted_at IS NULL
		ORDER BY s.created_at ASC
		LIMIT 1
	`
	var handle string
	err := db.QueryRowContext(ctx, query, name).Scan(&handle)
	return handle, err
}
func GetRetiredName(ctx context.Context, database *sql.DB, host string, f [2]string) (model.RetiredName, error) {
	var r model.RetiredName
	var siteID sql.NullString

	err := database.QueryRowContext(ctx, `SELECT site_id::text FROM legacy_hostnames WHERE hostname IN (lower($1), lower($2)) ORDER BY hostname = lower($1) DESC LIMIT 1`, f[0], f[1]).Scan(&siteID)
	r.SiteID = siteID.String
	return r, err
}
func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, domain string, f [2]string) (model.SiteDomainInfo, error) {

	info, err := ScanDomainInfo(database.QueryRowContext(ctx, `SELECT `+DomainColumns+` FROM sites WHERE custom_domain IN ($1, $2) AND deleted_at IS NULL ORDER BY custom_domain = $1 DESC LIMIT 1`, f[0], f[1]))
	if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	info, err = ScanDomainInfo(database.QueryRowContext(ctx, `SELECT `+DomainColumns+` FROM sites WHERE previous_domain IN ($1, $2) AND deleted_at IS NULL ORDER BY previous_domain = $1 DESC LIMIT 1`, f[0], f[1]))
	if err != nil {
		return model.SiteDomainInfo{}, err
	}
	return previousDomainInfo(info), nil
}
func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(u.handle, ''), u.id::text, s.name
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.id::text = $1 AND s.deleted_at IS NULL`, siteID).Scan(&handle, &userID, &name)
	return
}
func GetSiteOwnerEmail(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	var email string
	err := database.QueryRowContext(ctx, `SELECT u.username FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id = $1`, siteID).Scan(&email)
	return email, err
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
func ResolveOldSiteName(ctx context.Context, q Querier, userID, name string) (siteID, current string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT s.id, s.name
		  FROM site_name_aliases a JOIN sites s ON s.id = a.site_id
		 WHERE a.user_id::text = $1 AND a.name = $2
		   AND s.user_id = a.user_id AND s.deleted_at IS NULL`, userID, name).Scan(&siteID, &current)
	return siteID, current, err
}

func previousDomainInfo(info model.SiteDomainInfo) model.SiteDomainInfo {
	prev := info
	prev.Domain = info.PreviousDomain
	prev.Status = "active"
	prev.LastError = ""
	prev.CertStatus = "live"
	prev.FailingSince = sql.NullTime{}
	if !prev.VerifiedAt.Valid {
		prev.VerifiedAt = sql.NullTime{Time: time.Now(), Valid: true}
	}
	prev.PreviousDomain = ""
	return prev
}
