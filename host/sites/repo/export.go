package repo

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
