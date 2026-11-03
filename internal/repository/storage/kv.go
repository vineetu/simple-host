package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// KVItem holds a stored key and its original author.
type KVItem struct {
	Key      string
	Value    json.RawMessage
	WriterID string
}

// KVWriter reads the author of a key, including keys hidden by an author filter.
func KVWriter(ctx context.Context, db *sql.DB, siteID, resource, key string) (string, error) {
	var writer string
	err := db.QueryRowContext(ctx, `SELECT writer_id FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, siteID, resource, key).Scan(&writer)
	return writer, err
}

// KVSize reads the existing key's charged size.
func KVSize(ctx context.Context, db *sql.DB, siteID, resource, key string) (int64, error) {
	var size int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT octet_length(key)+octet_length(value::text) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3),0)`, siteID, resource, key).Scan(&size)
	return size, err
}

// KVNormalizedSize reads the size of PostgreSQL's normalized JSON value.
func KVNormalizedSize(ctx context.Context, db *sql.DB, value json.RawMessage) (int64, error) {
	var size int64
	err := db.QueryRowContext(ctx, `SELECT octet_length($1::jsonb::text)`, string(value)).Scan(&size)
	return size, err
}

// UpdateKV updates a key while retaining its author.
func UpdateKV(ctx context.Context, db *sql.DB, siteID, resource, key string, value json.RawMessage, filter string) (sql.Result, error) {
	return db.ExecContext(ctx, `UPDATE site_storage_kv SET value=$4, updated_at=now() WHERE site_id=$1 AND resource_name=$2 AND key=$3 AND ($5='' OR writer_id=$5)`, siteID, resource, key, value, filter)
}

// InsertKV creates a key only if it does not already exist.
func InsertKV(ctx context.Context, db *sql.DB, siteID, resource, key string, value json.RawMessage, writer string) (sql.Result, error) {
	return db.ExecContext(ctx, `INSERT INTO site_storage_kv(site_id,resource_name,key,value,writer_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(site_id,resource_name,key) DO NOTHING`, siteID, resource, key, value, writer)
}

// DeleteKV deletes a visible key.
func DeleteKV(ctx context.Context, db *sql.DB, siteID, resource, key, filter string) (sql.Result, error) {
	return db.ExecContext(ctx, `DELETE FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3 AND ($4='' OR writer_id=$4)`, siteID, resource, key, filter)
}

// GetKV reads one visible key.
func GetKV(ctx context.Context, db *sql.DB, siteID, resource, key, filter string) (KVItem, error) {
	var x KVItem
	x.Key = key
	err := db.QueryRowContext(ctx, `SELECT value, writer_id FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3 AND ($4='' OR writer_id=$4)`, siteID, resource, key, filter).Scan(&x.Value, &x.WriterID)
	return x, err
}

// ListKV reads visible keys in key order.
func ListKV(ctx context.Context, db *sql.DB, siteID, resource, prefix, after string, limit int, filter string) ([]KVItem, error) {
	prefix = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	rows, err := db.QueryContext(ctx, `SELECT key,value,writer_id FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key LIKE $3 ESCAPE '\' AND key>$4 AND ($6='' OR writer_id=$6) ORDER BY key LIMIT $5`, siteID, resource, prefix, after, limit, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KVItem{}
	for rows.Next() {
		var x KVItem
		if err = rows.Scan(&x.Key, &x.Value, &x.WriterID); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
