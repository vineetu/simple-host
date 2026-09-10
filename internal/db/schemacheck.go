package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// requiredColumns are the tables and columns this build reads.
//
// This list exists because of a real outage: a binary that queried
// users.display_name was deployed to a database where the migration adding it
// had never been run. Every user lookup then failed, so the owner page fell
// through to a 404 and site creation answered 500. Nothing failed at startup,
// and the fresh-install gate could not catch it, because that gate proves a NEW
// database works and says nothing about one that already exists.
//
// Add an entry here whenever a migration adds something the code depends on.
var requiredColumns = map[string][]string{
	"users":            {"id", "username", "api_key", "is_admin", "handle", "display_name", "handle_changed_at"},
	"sites":            {"id", "user_id", "name", "active_version", "visibility", "state", "custom_domain"},
	"collection_items": {"id", "site_id", "collection", "data"},
	"site_view_hourly": {"site_id", "hour", "class", "views"},
	"instance_config":  {"key", "value"},
}

// VerifySchema refuses to start against a database that is behind the code.
//
// A server that will not start is far better than one that starts and silently
// answers 404 for every page: the first is noticed in seconds, the second was
// noticed by the owner.
func VerifySchema(ctx context.Context, database *sql.DB) error {
	tables := make([]string, 0, len(requiredColumns))
	for t := range requiredColumns {
		tables = append(tables, t)
	}
	rows, err := database.QueryContext(ctx, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = ANY($1)`, pq.Array(tables))
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	defer rows.Close()

	have := map[string]map[string]bool{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return err
		}
		if have[t] == nil {
			have[t] = map[string]bool{}
		}
		have[t][c] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var missing []string
	for table, cols := range requiredColumns {
		if have[table] == nil {
			missing = append(missing, table+" (whole table)")
			continue
		}
		for _, c := range cols {
			if !have[table][c] {
				missing = append(missing, table+"."+c)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("database is behind this build; missing: %s\n"+
			"Apply the migrations in db/migrations/, or db/schema.sql on a new database",
			strings.Join(missing, ", "))
	}
	return nil
}
