package sites

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
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

type exportQuerier interface {
	Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
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

// ExportKVNamespaces lists the declared KV files, in resource order.
func ExportKVNamespaces(ctx context.Context, q exportQuerier, siteID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM site_storage_resources WHERE site_id=$1 AND kind='kv' ORDER BY name`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// OrderExportKV sorts by the Postgres resource/key collation the archive has always used.
func OrderExportKV(ctx context.Context, q exportQuerier, entries []ExportKVEntry) ([]ExportKVEntry, error) {
	resources, keys := make([]string, len(entries)), make([]string, len(entries))
	for i, e := range entries {
		resources[i], keys[i] = e.Resource, e.Key
	}
	rows, err := q.QueryContext(ctx, `SELECT ord-1 FROM unnest($1::text[],$2::text[]) WITH ORDINALITY AS e(resource,key,ord) ORDER BY resource,key,ord`, pq.Array(resources), pq.Array(keys))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ExportKVEntry, 0, len(entries))
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return nil, err
		}
		out = append(out, entries[i])
	}
	return out, rows.Err()
}
