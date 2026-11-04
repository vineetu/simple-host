package sites

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ExportOwner reads the identity used to lock a site during export.
func ExportOwner(ctx context.Context, q Querier, siteID string) (ownerID, siteName string, err error) {
	err = q.QueryRowContext(ctx, `SELECT user_id::text,name FROM sites WHERE id=$1`, siteID).Scan(&ownerID, &siteName)
	return
}

// ExportKVEntry is one saved KV value with its author.
type ExportKVEntry struct {
	Resource string          `json:"resource"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	WriterID string          `json:"writer_id"`
}

// ExportFileOwners returns file authors in archive order.
func ExportFileOwners(ctx context.Context, q exportQuerier, siteID string) ([]map[string]string, error) {
	fileRows, e := q.QueryContext(ctx, `SELECT resource_name,path,writer_id FROM site_storage_files WHERE site_id=$1 ORDER BY resource_name,path`, siteID)
	if e != nil {
		return nil, e
	}
	fileOwners := []map[string]string{}
	for fileRows.Next() {
		var resource, path, writer string
		if e = fileRows.Scan(&resource, &path, &writer); e != nil {
			fileRows.Close()
			return nil, e
		}
		fileOwners = append(fileOwners, map[string]string{"resource": resource, "path": path, "writer_id": writer})
	}
	e = fileRows.Err()
	fileRows.Close()
	if e != nil {
		return nil, e
	}
	return fileOwners, nil
}

// ExportKV returns KV values and authors in archive order.
func ExportKV(ctx context.Context, q exportQuerier, siteID string) ([]ExportKVEntry, error) {
	kvRows, err := q.QueryContext(ctx, `SELECT resource_name,key,value,writer_id FROM site_storage_kv WHERE site_id=$1 ORDER BY resource_name,key`, siteID)
	if err != nil {
		return nil, err
	}
	entries := []ExportKVEntry{}
	for kvRows.Next() {
		var x ExportKVEntry
		if err := kvRows.Scan(&x.Resource, &x.Key, &x.Value, &x.WriterID); err != nil {
			kvRows.Close()
			return nil, err
		}
		entries = append(entries, x)
	}
	err = kvRows.Err()
	kvRows.Close()
	if err != nil {
		return nil, err
	}
	return entries, nil
}

type exportQuerier interface {
	Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ExportItem is one list item as the export writes it.
type ExportItem struct {
	ID          int64
	Collection  string
	Data        []byte
	CreatedAt   time.Time
	SubmittedBy string // the identity the visitor sent it as, "" for public lists
}

// ListAllSites lists all live sites with their owners and export metadata.
func ListAllSites(ctx context.Context, db *sql.DB) ([]Site, error) {
	const query = `
		SELECT s.id, s.user_id, s.name, s.active_version, COALESCE(s.site_url, ''), s.created_at, s.updated_at, s.custom_domain, s.domain_status, s.visibility, u.username, COALESCE(u.handle, ''),
		       s.domain_last_error, s.domain_bound_at, s.domain_verified_at, COALESCE(s.previous_domain, ''), COALESCE(s.domain_cert_status, ''), COALESCE(s.domain_token, ''), (SELECT max(v.created_at) FROM versions v WHERE v.site_id = s.id AND v.status = 'active'),
		       s.suspended_at IS NOT NULL, COALESCE(s.suspended_reason, ''),
		       u.suspended_at IS NOT NULL, COALESCE(u.suspended_reason, ''),
		       s.offline_at IS NOT NULL, s.keep_versions, s.passcode_enc IS NOT NULL, s.access = 'specific',
		       COALESCE(s.summary_title, ''), COALESCE(s.summary_description, ''), COALESCE(s.summary_headings, ''), COALESCE(s.summary_text, '')
		FROM sites s
		INNER JOIN users u ON u.id = s.user_id
		WHERE s.deleted_at IS NULL
		ORDER BY s.updated_at DESC, s.created_at DESC, s.name ASC
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site
		if err := rows.Scan(
			&site.ID,
			&site.UserID,
			&site.Name,
			&site.ActiveVersion,
			&site.SiteURL,
			&site.CreatedAt,
			&site.UpdatedAt,
			&site.CustomDomain,
			&site.DomainStatus,
			&site.Visibility,
			&site.OwnerUsername,
			&site.OwnerHandle,
			&site.DomainLastError,
			&site.DomainBoundAt,
			&site.DomainVerifiedAt,
			&site.PreviousDomain,
			&site.DomainCertStatus,
			&site.DomainToken,
			&site.LastDeployedAt,
			&site.SiteSuspended,
			&site.SiteSuspendedReason,
			&site.OwnerSuspended,
			&site.OwnerSuspendedReason,
			&site.Offline,
			&site.KeepVersions,
			&site.Passcode,
			&site.NamedViewers,
			&site.SummaryTitle, &site.SummaryDescription, &site.SummaryHeadings, &site.SummaryText,
		); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sites, nil
}

// GetSiteStateByID reads the site state and version by ID.
func GetSiteStateByID(ctx context.Context, db *sql.DB, siteID string) (json.RawMessage, int, error) {
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE id = $1`

	var state []byte
	var version int
	if err := db.QueryRowContext(ctx, query, siteID).Scan(&state, &version); err != nil {
		return nil, 0, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), version, nil
	}
	return json.RawMessage(state), version, nil
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
