package storage

import (
	"context"
	"database/sql"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

// KVUsage reads the durable bytes used by a site's KV entries.
func KVUsage(ctx context.Context, db *sql.DB, siteID string, used *int64) error {
	return db.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(key)+octet_length(value::text)),0) FROM site_storage_kv WHERE site_id=$1`, siteID).Scan(used)
}

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
		if out[i].Kind == "sqlite" && out[i].Access != nil {
			if out[i].Tables, err = LoadTables(ctx, db, siteID, out[i].Name); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// ClearResourceWriters clears legacy authors within the resource transaction.
func ClearResourceWriters(ctx context.Context, tx *sql.Tx, table, siteID, name string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE `+table+` SET writer_id='' WHERE site_id=$1 AND resource_name=$2`, siteID, name)
}

// UpsertResource saves a resource without changing an existing resource's kind.
func UpsertResource(ctx context.Context, tx *sql.Tx, siteID string, x rules.Resource) (sql.Result, error) {
	m := x.Access
	return tx.ExecContext(ctx, `INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(site_id,name) DO UPDATE SET read_policy=EXCLUDED.read_policy,write_policy=EXCLUDED.write_policy,site_passcode=EXCLUDED.site_passcode,write_mode=EXCLUDED.write_mode,acc_read=EXCLUDED.acc_read,acc_add=EXCLUDED.acc_add,acc_edit=EXCLUDED.acc_edit,acc_delete=EXCLUDED.acc_delete,preset_base=EXCLUDED.preset_base,approve_first=EXCLUDED.approve_first WHERE site_storage_resources.kind=EXCLUDED.kind`, siteID, x.Name, x.Kind, x.Read, x.Write, x.SitePasscode, x.WriteMode, m.Read, m.Add, m.Edit, m.Delete, x.PresetBase, x.ApproveFirst)
}

// DeleteResourceTables removes the existing per-table rules.
func DeleteResourceTables(ctx context.Context, tx *sql.Tx, siteID, name string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
}

// InsertResourceTable saves one per-table rule.
func InsertResourceTable(ctx context.Context, tx *sql.Tx, siteID, name, table string, t rules.TableRule) (sql.Result, error) {
	return tx.ExecContext(ctx, `INSERT INTO site_storage_tables(site_id,resource_name,table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, siteID, name, table, t.Matrix.Read, t.Matrix.Add, t.Matrix.Edit, t.Matrix.Delete, t.PresetBase)
}

// DeleteResource removes a resource within its transaction.
func DeleteResource(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name)
	return err
}
