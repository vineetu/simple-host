package repo

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/vsriram/simple-host/host/data/rules"
)

// ListResources reads resources in name order, followed by their table rules.
func ListResources(ctx context.Context, db *sql.DB, siteID string) ([]Resource, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+ResourceColumns+` FROM site_storage_resources WHERE site_id=$1 ORDER BY name`, siteID)
	if err != nil {
		return nil, err
	}
	out := []Resource{}
	for rows.Next() {
		x, err := ScanResource(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Kind == "sqlite" && out[i].Rules != nil {
			if out[i].Tables, err = LoadTables(ctx, db, siteID, out[i].Name); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// ClearFileWriters clears legacy authors within the resource transaction.
func ClearFileWriters(ctx context.Context, tx *sql.Tx, siteID, name string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE site_storage_files SET writer_id='' WHERE site_id=$1 AND resource_name=$2`, siteID, name)
}

// UpsertResource saves a resource without changing an existing resource's
// kind. A resource with no stored rules (saved before rules) changes only its
// site_passcode. A rules save also sets acc_read, which marks the resource as
// saved after presets (ResourceColumns reads it as BeforePresets).
func UpsertResource(ctx context.Context, tx *sql.Tx, siteID string, x rules.Resource) (sql.Result, error) {
	if x.Rules == nil {
		return tx.ExecContext(ctx, `UPDATE site_storage_resources SET site_passcode=$3 WHERE site_id=$1 AND name=$2 AND kind=$4`, siteID, x.Name, x.SitePasscode, x.Kind)
	}
	return tx.ExecContext(ctx, `INSERT INTO site_storage_resources(site_id,name,kind,site_passcode,acc_read,rules) VALUES($1,$2,$3,$4,'nobody',$5) ON CONFLICT(site_id,name) DO UPDATE SET site_passcode=EXCLUDED.site_passcode,acc_read=EXCLUDED.acc_read,rules=EXCLUDED.rules WHERE site_storage_resources.kind=EXCLUDED.kind`, siteID, x.Name, x.Kind, x.SitePasscode, nullableJSON(x.Rules))
}

// DeleteResourceTables removes the existing per-table rules.
func DeleteResourceTables(ctx context.Context, tx *sql.Tx, siteID, name string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
}

// InsertResourceTable saves one per-table rule set.
func InsertResourceTable(ctx context.Context, tx *sql.Tx, siteID, name, table string, t rules.TableRule) (sql.Result, error) {
	return tx.ExecContext(ctx, `INSERT INTO site_storage_tables(site_id,resource_name,table_name,rules) VALUES($1,$2,$3,$4)`, siteID, name, table, nullableJSON(t.Rules))
}

// DeleteResource removes a resource within its transaction.
func DeleteResource(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name)
	return err
}

// A typed nil byte slice is an empty JSON parameter to lib/pq, not SQL NULL.
func nullableJSON(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

// KVNamespaces enumerates declared files only; orphan files are not usage.
func KVNamespaces(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM site_storage_resources WHERE site_id=$1 AND kind='kv' ORDER BY name`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
