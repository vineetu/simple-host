package sites

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// LoadSiteTable takes one consistent, read-only snapshot. All result sets are
// closed before the next query on the transaction. No credential, session,
// viewer list, generation or protection decision is copied.
func LoadSiteTable(ctx context.Context, db *sql.DB, groups func(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error)) (model.SiteTable, time.Time, error) {
	var table model.SiteTable
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return table, time.Time{}, err
	}
	defer tx.Rollback()
	var started time.Time
	if err = tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&started); err != nil {
		return table, started, err
	}
	read := func(query string, scan func(*sql.Rows) error) error {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err = scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	err = read(`SELECT id, COALESCE(handle,''), COALESCE(home_site_id::text,'') FROM users ORDER BY id`, func(r *sql.Rows) error {
		var v model.RouteOwner
		err := r.Scan(&v.ID, &v.Handle, &v.HomeSiteID)
		table.Owners = append(table.Owners, v)
		return err
	})
	if err != nil {
		return table, started, err
	}
	err = read(`SELECT s.id,s.user_id,s.name,s.created_at,s.active_version,s.deleted_at IS NOT NULL,
 (s.passcode_enc IS NOT NULL OR s.access='specific' OR s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL),
 (s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL),
 COALESCE(s.custom_domain,''),COALESCE(s.domain_status,''),s.domain_verified_at,COALESCE(s.previous_domain,'')
 FROM sites s JOIN users u ON u.id=s.user_id ORDER BY s.created_at,s.id`, func(r *sql.Rows) error {
		var v model.RouteSite
		err := r.Scan(&v.ID, &v.UserID, &v.Name, &v.CreatedAt, &v.ActiveVersion, &v.Deleted, &v.RestrictedResponse, &v.HomeUnavailable, &v.Domain.Domain, &v.Domain.Status, &v.Domain.VerifiedAt, &v.Domain.PreviousDomain)
		v.Domain.SiteID, v.Domain.UserID, v.Domain.Name = v.ID, v.UserID, v.Name
		table.Sites = append(table.Sites, v)
		return err
	})
	if err != nil {
		return table, started, err
	}
	err = read(`SELECT a.user_id,a.handle,u.handle FROM handle_aliases a JOIN users u ON u.id=a.user_id WHERE u.handle IS NOT NULL ORDER BY a.handle`, func(r *sql.Rows) error {
		var v model.RouteAlias
		err := r.Scan(&v.OwnerID, &v.Name, &v.Target)
		table.HandleAliases = append(table.HandleAliases, v)
		return err
	})
	if err != nil {
		return table, started, err
	}
	err = read(`SELECT a.user_id,a.name,s.id FROM site_name_aliases a JOIN sites s ON s.id=a.site_id WHERE a.user_id=s.user_id AND s.deleted_at IS NULL ORDER BY a.user_id,a.name`, func(r *sql.Rows) error {
		var v model.RouteAlias
		err := r.Scan(&v.OwnerID, &v.Name, &v.Target)
		table.SiteAliases = append(table.SiteAliases, v)
		return err
	})
	if err != nil {
		return table, started, err
	}
	err = read(`SELECT hostname,COALESCE(site_id::text,'') FROM legacy_hostnames ORDER BY hostname`, func(r *sql.Rows) error {
		var v model.RouteRetired
		err := r.Scan(&v.Host, &v.SiteID)
		table.Retired = append(table.Retired, v)
		return err
	})
	if err != nil {
		return table, started, err
	}
	err = read(`SELECT id,user_id,suffix,site_prefix,rank,canonical,status,verified_at,cert_mode,COALESCE(cert_name,''),proof_exempt FROM address_families ORDER BY suffix,created_at`, func(r *sql.Rows) error {
		var f model.AddressFamily
		err := r.Scan(&f.ID, &f.UserID, &f.Suffix, &f.SitePrefix, &f.Rank, &f.Canonical, &f.Status, &f.VerifiedAt, &f.CertMode, &f.CertName, &f.ProofExempt)
		table.Families = append(table.Families, f)
		return err
	})
	if err != nil {
		return table, started, err
	}
	if groups != nil {
		table.Groups, table.GroupSites, err = groups(ctx, tx)
		if err != nil {
			return model.SiteTable{}, started, err
		}
	}
	if err = tx.Commit(); err != nil {
		return model.SiteTable{}, started, err
	}
	return table, started, nil
}

// LookupRoutingSite is the single bounded miss query. It returns only the
// address's owner and site, with current domains preferred over previous ones,
// then a family address. A family absent from the copy is returned as well;
// the handler still applies its label and on-disk readiness checks. The caller
// adds routing facts to its copy, never an access grant.
func LookupRoutingSite(ctx context.Context, db *sql.DB, address model.RoutingAddress, forms [2]string) (model.SiteTable, error) {
	var table model.SiteTable
	var u model.RouteOwner
	var s model.RouteSite
	var f model.AddressFamily
	err := db.QueryRowContext(ctx, `SELECT u.id,COALESCE(u.handle,''),COALESCE(u.home_site_id::text,''),
 s.id,s.user_id,s.name,s.created_at,s.active_version,
 (s.passcode_enc IS NOT NULL OR s.access='specific' OR s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL),
 (s.offline_at IS NOT NULL OR s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL),
 COALESCE(s.custom_domain,''),COALESCE(s.domain_status,''),s.domain_verified_at,COALESCE(s.previous_domain,''),
 COALESCE(f.id::text,''),COALESCE(f.user_id::text,''),COALESCE(f.suffix,''),COALESCE(f.site_prefix,''),
 COALESCE(f.rank,0),COALESCE(f.canonical,false),COALESCE(f.status,''),f.verified_at,
 COALESCE(f.cert_mode,''),COALESCE(f.cert_name,''),COALESCE(f.proof_exempt,false)
 FROM sites s JOIN users u ON u.id=s.user_id
 LEFT JOIN address_families f ON $3<>'' AND strpos($3,'.')>1
 AND f.suffix=substr($3,strpos($3,'.')+1) AND f.user_id=s.user_id
 AND s.name=f.site_prefix || split_part($3,'.',1)
 WHERE s.deleted_at IS NULL AND
 (($3='' AND u.handle=$1 AND s.name=$2) OR ($3<>'' AND (s.custom_domain IN ($3,$4) OR s.previous_domain IN ($3,$4) OR f.id IS NOT NULL)))
 ORDER BY CASE WHEN s.custom_domain=$3 THEN 0 WHEN s.custom_domain=$4 THEN 1
 WHEN s.previous_domain=$3 THEN 2 WHEN s.previous_domain=$4 THEN 3 ELSE 4 END LIMIT 1`, address.Handle, address.Name, forms[0], forms[1]).Scan(
		&u.ID, &u.Handle, &u.HomeSiteID, &s.ID, &s.UserID, &s.Name, &s.CreatedAt, &s.ActiveVersion, &s.RestrictedResponse, &s.HomeUnavailable,
		&s.Domain.Domain, &s.Domain.Status, &s.Domain.VerifiedAt, &s.Domain.PreviousDomain,
		&f.ID, &f.UserID, &f.Suffix, &f.SitePrefix, &f.Rank, &f.Canonical, &f.Status, &f.VerifiedAt,
		&f.CertMode, &f.CertName, &f.ProofExempt)
	if err != nil {
		return table, err
	}
	s.Domain.SiteID, s.Domain.UserID, s.Domain.Name = s.ID, s.UserID, s.Name
	table.Owners = []model.RouteOwner{u}
	table.Sites = []model.RouteSite{s}
	if f.ID != "" {
		table.Families = []model.AddressFamily{f}
	}
	return table, nil
}
