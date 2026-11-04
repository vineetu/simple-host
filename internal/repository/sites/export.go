package sites

import (
	"context"
	"database/sql"
	"encoding/json"
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
