package hackevents

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
)

// LoadRoutingFacts joins the caller's read-only routing snapshot. No event
// gates, member lists, codes or credentials enter the copy.
func LoadRoutingFacts(ctx context.Context, tx *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,slug,account_id FROM events ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	var groups []model.RouteGroup
	for rows.Next() {
		var g model.RouteGroup
		if err = rows.Scan(&g.ID, &g.Slug, &g.AccountID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		groups = append(groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT event_id,slug FROM event_teams ORDER BY event_id,slug`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var sites []model.RouteGroupSite
	for rows.Next() {
		var s model.RouteGroupSite
		if err = rows.Scan(&s.GroupID, &s.Name); err != nil {
			return nil, nil, err
		}
		sites = append(sites, s)
	}
	return groups, sites, rows.Err()
}

// LookupRoutingFacts answers one uncopied group/team TLS question with one
// read-only query. A group needs no website to have its own TLS address.
func LookupRoutingFacts(ctx context.Context, db *sql.DB, slug, team string) (model.SiteTable, error) {
	var table model.SiteTable
	var group model.RouteGroup
	var owner model.RouteOwner
	var site model.RouteSite
	var created sql.NullTime
	var teamName string
	err := db.QueryRowContext(ctx, `SELECT e.id,e.slug,e.account_id,
 u.id,COALESCE(u.handle,''),COALESCE(u.home_site_id::text,''),COALESCE(t.slug,''),
 COALESCE(s.id::text,''),COALESCE(s.user_id::text,''),COALESCE(s.name,''),s.created_at,COALESCE(s.active_version,0),
 COALESCE(s.passcode_enc IS NOT NULL OR s.access='specific' OR s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL,false),
 COALESCE(s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL,false),
 COALESCE(s.custom_domain,''),COALESCE(s.domain_status,''),s.domain_verified_at,COALESCE(s.previous_domain,'')
 FROM events e JOIN users u ON u.id=e.account_id
 LEFT JOIN event_teams t ON t.event_id=e.id AND t.slug=$2
 LEFT JOIN sites s ON s.user_id=e.account_id AND s.name=t.slug AND s.deleted_at IS NULL
 WHERE e.slug=$1`, slug, team).Scan(&group.ID, &group.Slug, &group.AccountID,
		&owner.ID, &owner.Handle, &owner.HomeSiteID, &teamName,
		&site.ID, &site.UserID, &site.Name, &created, &site.ActiveVersion, &site.RestrictedResponse, &site.HomeUnavailable,
		&site.Domain.Domain, &site.Domain.Status, &site.Domain.VerifiedAt, &site.Domain.PreviousDomain)
	if err != nil {
		return table, err
	}
	table.Groups = []model.RouteGroup{group}
	table.Owners = []model.RouteOwner{owner}
	if teamName != "" {
		table.GroupSites = []model.RouteGroupSite{{GroupID: group.ID, Name: teamName}}
	}
	if site.ID != "" {
		site.CreatedAt = created.Time
		site.Domain.SiteID, site.Domain.UserID, site.Domain.Name = site.ID, site.UserID, site.Name
		table.Sites = []model.RouteSite{site}
	}
	return table, nil
}
