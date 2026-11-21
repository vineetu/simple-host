package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func DomainHeldByDeletedSite(ctx context.Context, q Querier, host string, f [2]string) (bool, error) {
	var held bool

	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE deleted_at IS NOT NULL
		AND (custom_domain IN (lower($1), lower($2)) OR previous_domain IN (lower($1), lower($2))))`, f[0], f[1]).Scan(&held)
	return held, err
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
	return info.PreviousDomainView(time.Now()), nil
}
func HomeSite(ctx context.Context, q Querier, userID string) (model.Site, bool, error) {
	id, err := accountsrepo.HomeSiteID(ctx, q, userID)
	if err != nil {
		return model.Site{}, false, err
	}
	if !id.Valid {
		return model.Site{}, false, nil
	}
	s, err := GetSiteByID(ctx, q, id.String)
	if err == sql.ErrNoRows || (err == nil && s.Deleted) {
		return model.Site{}, false, accountsrepo.ClearHomeSite(ctx, q, id.String)
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
