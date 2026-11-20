package repo

import (
	"context"
	"database/sql"
	"encoding/json"
)

// KVNormalize returns PostgreSQL's canonical JSON text for a value before it
// is written to the namespace's SQLite file.
func KVNormalize(ctx context.Context, db *sql.DB, value json.RawMessage) (string, error) {
	var text string
	err := db.QueryRowContext(ctx, `SELECT $1::jsonb::text`, string(value)).Scan(&text)
	return text, err
}
