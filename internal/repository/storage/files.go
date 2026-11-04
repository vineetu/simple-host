package storage

import (
	"context"
	"database/sql"
	"github.com/lib/pq"
	"strings"
)

// FileOwned reports whether the specified visitor authored a file.
func FileOwned(ctx context.Context, db *sql.DB, site, resource, path, visitor string) (bool, error) {
	var mine bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3 AND writer_id=$4)`, site, resource, path, visitor).Scan(&mine)
	return mine, err
}

// DeleteFile deletes a file's author record.
func DeleteFile(ctx context.Context, db *sql.DB, site, resource, path string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3`, site, resource, path)
	return err
}

// FileWriter returns the recorded author of a file.
func FileWriter(ctx context.Context, db *sql.DB, site, resource, path string) (string, error) {
	var writer string
	err := db.QueryRowContext(ctx, `SELECT writer_id FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3`, site, resource, path).Scan(&writer)
	return writer, err
}

// RecordFileEdit ensures an edited file has an author record.
func RecordFileEdit(ctx context.Context, db *sql.DB, site, resource, path string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,$2,$3,'') ON CONFLICT(site_id,resource_name,path) DO NOTHING`, site, resource, path)
	return err
}

// RecordFileAdd records the author of a newly published file.
func RecordFileAdd(ctx context.Context, db *sql.DB, site, resource, path, writer string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,$2,$3,$4) ON CONFLICT(site_id,resource_name,path) DO UPDATE SET writer_id=EXCLUDED.writer_id, created_at=now()`, site, resource, path, writer)
	return err
}

// ForEachFilePath visits a visitor's paths in pagination order while the cursor is open.
func ForEachFilePath(ctx context.Context, db *sql.DB, site, resource, writer, after, prefix string, limit int, fn func(string) (bool, error)) error {
	prefix = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	rows, err := db.QueryContext(ctx, `SELECT path FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND writer_id=$3 AND path>$4 AND path LIKE $5 ESCAPE '\' ORDER BY path LIMIT $6`, site, resource, writer, after, prefix, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		cont, err := fn(p)
		if err != nil {
			return err
		}
		if !cont {
			break
		}
	}
	return rows.Err()
}

// FileWriters reads the authors of the given paths.
func FileWriters(ctx context.Context, db *sql.DB, site, resource string, paths []string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT path, writer_id FROM site_storage_files WHERE site_id = $1 AND resource_name = $2 AND path = ANY($3)`, site, resource, pq.Array(paths))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, w string
		if err := rows.Scan(&p, &w); err != nil {
			return nil, err
		}
		out[p] = w
	}
	return out, rows.Err()
}
