package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
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
		if out[i].Kind == "kv" {
			if err = db.QueryRowContext(ctx, `SELECT kv_store FROM site_storage_resources WHERE site_id=$1 AND name=$2 AND kind='kv'`, siteID, out[i].Name).Scan(&out[i].KVStore); err != nil {
				return nil, err
			}
		}
		if out[i].Kind == "sqlite" && (out[i].Access != nil || out[i].Rules != nil) {
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
	if x.KVStore == "" {
		x.KVStore = "postgres"
	}
	return tx.ExecContext(ctx, `INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first,rules,rules_converted_from,kv_store) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(site_id,name) DO UPDATE SET read_policy=EXCLUDED.read_policy,write_policy=EXCLUDED.write_policy,site_passcode=EXCLUDED.site_passcode,write_mode=EXCLUDED.write_mode,acc_read=EXCLUDED.acc_read,acc_add=EXCLUDED.acc_add,acc_edit=EXCLUDED.acc_edit,acc_delete=EXCLUDED.acc_delete,preset_base=EXCLUDED.preset_base,approve_first=EXCLUDED.approve_first,rules=EXCLUDED.rules,rules_converted_from=EXCLUDED.rules_converted_from WHERE site_storage_resources.kind=EXCLUDED.kind`, siteID, x.Name, x.Kind, x.Read, x.Write, x.SitePasscode, x.WriteMode, m.Read, m.Add, m.Edit, m.Delete, x.PresetBase, x.ApproveFirst, nullableJSON(x.Rules), nullableJSON(x.ConvertedFrom), x.KVStore)
}

// DeleteResourceTables removes the existing per-table rules.
func DeleteResourceTables(ctx context.Context, tx *sql.Tx, siteID, name string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
}

// InsertResourceTable saves one per-table rule.
func InsertResourceTable(ctx context.Context, tx *sql.Tx, siteID, name, table string, t rules.TableRule) (sql.Result, error) {
	return tx.ExecContext(ctx, `INSERT INTO site_storage_tables(site_id,resource_name,table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base,rules) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, siteID, name, table, t.Matrix.Read, t.Matrix.Add, t.Matrix.Edit, t.Matrix.Delete, t.PresetBase, nullableJSON(t.Rules))
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

// SQLiteKVNamespaces enumerates declared files only; orphan files are not usage.
func SQLiteKVNamespaces(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM site_storage_resources WHERE site_id=$1 AND kind='kv' AND kv_store='sqlite' ORDER BY name`, siteID)
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

// ConvertResource is one SQLite or key-value resource still on presets, with
// the facts migrate-rules needs to name it and to find its site directory.
type ConvertResource struct {
	SiteID, Name, Kind, OwnerID, SiteName, Account string
	Deleted                                        bool
}

// ConvertScope lists the resources migrate-rules may look at: SQLite and
// key-value resources with no rules. File buckets never take rules and are
// never listed. accounts nil means every account.
func ConvertScope(ctx context.Context, db *sql.DB, accounts []string) ([]ConvertResource, error) {
	query := `SELECT r.site_id,r.name,r.kind,s.user_id,s.name,COALESCE(u.handle,''),s.deleted_at IS NOT NULL FROM site_storage_resources r JOIN sites s ON s.id=r.site_id JOIN users u ON u.id=s.user_id WHERE r.kind IN ('sqlite','kv') AND r.rules IS NULL`
	var args []any
	if accounts != nil {
		query += ` AND u.handle = ANY($1)`
		args = append(args, pq.Array(accounts))
	}
	rows, err := db.QueryContext(ctx, query+` ORDER BY u.handle NULLS LAST,s.name,r.site_id,r.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConvertResource
	for rows.Next() {
		var r ConvertResource
		if err := rows.Scan(&r.SiteID, &r.Name, &r.Kind, &r.OwnerID, &r.SiteName, &r.Account, &r.Deleted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ConvertTarget reads the same facts for one resource, whatever its state.
func ConvertTarget(ctx context.Context, db Querier, siteID, name string) (ConvertResource, error) {
	r := ConvertResource{SiteID: siteID, Name: name}
	err := db.QueryRowContext(ctx, `SELECT r.kind,s.user_id,s.name,COALESCE(u.handle,''),s.deleted_at IS NOT NULL FROM site_storage_resources r JOIN sites s ON s.id=r.site_id JOIN users u ON u.id=s.user_id WHERE r.site_id=$1 AND r.name=$2`, siteID, name).Scan(&r.Kind, &r.OwnerID, &r.SiteName, &r.Account, &r.Deleted)
	return r, err
}

// AccountHandleExists reports whether an account has this handle.
func AccountHandleExists(ctx context.Context, db *sql.DB, handle string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE handle=$1)`, handle).Scan(&ok)
	return ok, err
}

// RecordConvertedFrom is step 1 of a conversion: it stores what the resource
// was, and changes nothing a request reads. It reports whether a row matched.
func RecordConvertedFrom(ctx context.Context, db Querier, siteID, name string, record json.RawMessage) (bool, error) {
	result, err := db.ExecContext(ctx, `UPDATE site_storage_resources SET rules_converted_from=$3 WHERE site_id=$1 AND name=$2 AND rules IS NULL`, siteID, name, string(record))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ErrConvertRowCount means a conversion step did not change exactly one
// resource row; its transaction was rolled back.
var ErrConvertRowCount = errors.New("expected exactly one resource row to change")

// ConvertToRules is step 3 of a conversion, in one transaction: the default
// set and the fill on the resource (only while it has no rules), and its
// table rows replaced by the given ones.
func ConvertToRules(ctx context.Context, db KVMoveDB, siteID, name string, set json.RawMessage, tables map[string]rules.TableRule, beforeCommit func() error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE site_storage_resources SET read_policy='owner',write_policy='owner',write_mode='add',acc_read='nobody',acc_add='nobody',acc_edit='nobody',acc_delete='nobody',preset_base='rules',approve_first=FALSE,rules=$3 WHERE site_id=$1 AND name=$2 AND kind IN ('sqlite','kv') AND rules IS NULL AND rules_converted_from IS NOT NULL`, siteID, name, string(set))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.Join(ErrConvertRowCount, err)
	}
	if _, err = DeleteResourceTables(ctx, tx, siteID, name); err != nil {
		return err
	}
	for table, t := range tables {
		if _, err = InsertResourceTable(ctx, tx, siteID, name, table, t); err != nil {
			return err
		}
	}
	if beforeCommit != nil {
		if err = beforeCommit(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ConvertOriginal is the configuration a resource had before conversion, as
// its columns held it. A nil acc value is SQL NULL (saved before presets).
type ConvertOriginal struct {
	ReadPolicy   string                 `json:"read_policy"`
	WritePolicy  string                 `json:"write_policy"`
	WriteMode    string                 `json:"write_mode"`
	AccRead      *string                `json:"acc_read"`
	AccAdd       *string                `json:"acc_add"`
	AccEdit      *string                `json:"acc_edit"`
	AccDelete    *string                `json:"acc_delete"`
	PresetBase   string                 `json:"preset_base"`
	ApproveFirst bool                   `json:"approve_first"`
	Tables       []ConvertOriginalTable `json:"tables"`
}

// ConvertOriginalTable is one table row of the original configuration.
type ConvertOriginalTable struct {
	Table      string `json:"table_name"`
	AccRead    string `json:"acc_read"`
	AccAdd     string `json:"acc_add"`
	AccEdit    string `json:"acc_edit"`
	AccDelete  string `json:"acc_delete"`
	PresetBase string `json:"preset_base"`
}

// LoadConvertOriginal reads the columns a conversion replaces, with every
// table row in name order, whatever the resource's state.
func LoadConvertOriginal(ctx context.Context, db Querier, siteID, name string) (ConvertOriginal, error) {
	var o ConvertOriginal
	var r, a, e, d sql.NullString
	err := db.QueryRowContext(ctx, `SELECT read_policy,write_policy,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name).Scan(&o.ReadPolicy, &o.WritePolicy, &o.WriteMode, &r, &a, &e, &d, &o.PresetBase, &o.ApproveFirst)
	if err != nil {
		return o, err
	}
	for _, f := range []struct {
		from sql.NullString
		to   **string
	}{{r, &o.AccRead}, {a, &o.AccAdd}, {e, &o.AccEdit}, {d, &o.AccDelete}} {
		if f.from.Valid {
			v := f.from.String
			*f.to = &v
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2 ORDER BY table_name`, siteID, name)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	o.Tables = []ConvertOriginalTable{}
	for rows.Next() {
		var t ConvertOriginalTable
		if err := rows.Scan(&t.Table, &t.AccRead, &t.AccAdd, &t.AccEdit, &t.AccDelete, &t.PresetBase); err != nil {
			return o, err
		}
		o.Tables = append(o.Tables, t)
	}
	return o, rows.Err()
}

// RevertFromRules restores the recorded configuration in one transaction:
// every resource column and table row as it was, no rules on either, and the
// record cleared.
func RevertFromRules(ctx context.Context, db KVMoveDB, siteID, name string, o ConvertOriginal, beforeCommit func() error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	null := func(v *string) any {
		if v == nil {
			return nil
		}
		return *v
	}
	result, err := tx.ExecContext(ctx, `UPDATE site_storage_resources SET read_policy=$3,write_policy=$4,write_mode=$5,acc_read=$6,acc_add=$7,acc_edit=$8,acc_delete=$9,preset_base=$10,approve_first=$11,rules=NULL,rules_converted_from=NULL WHERE site_id=$1 AND name=$2 AND kind IN ('sqlite','kv') AND rules IS NOT NULL AND rules_converted_from IS NOT NULL`, siteID, name, o.ReadPolicy, o.WritePolicy, o.WriteMode, null(o.AccRead), null(o.AccAdd), null(o.AccEdit), null(o.AccDelete), o.PresetBase, o.ApproveFirst)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.Join(ErrConvertRowCount, err)
	}
	if _, err = DeleteResourceTables(ctx, tx, siteID, name); err != nil {
		return err
	}
	for _, t := range o.Tables {
		if _, err = InsertResourceTable(ctx, tx, siteID, name, t.Table, rules.TableRule{Matrix: rules.AccessMatrix{Read: t.AccRead, Add: t.AccAdd, Edit: t.AccEdit, Delete: t.AccDelete}, PresetBase: t.PresetBase}); err != nil {
			return err
		}
	}
	if beforeCommit != nil {
		if err = beforeCommit(); err != nil {
			return err
		}
	}
	return tx.Commit()
}
