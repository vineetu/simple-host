package storage

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

// Querier supports resource queries on a database or transaction.
type Querier interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Resource is the storage resource record shared with policy rules.
type Resource = rules.Resource

// ResourceColumns is the ordered list of columns read by ScanResource.
const ResourceColumns = `name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first,rules,rules_converted_from`

// ScanResource scans the resource columns from a row.
func ScanResource(scan func(...any) error) (Resource, error) {
	var x Resource
	var r, a, e, d sql.NullString
	var rawRules, convertedFrom []byte
	if err := scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode, &x.WriteMode, &r, &a, &e, &d, &x.PresetBase, &x.ApproveFirst, &rawRules, &convertedFrom); err != nil {
		return x, err
	}
	x.Rules, x.ConvertedFrom = rawRules, convertedFrom
	if r.Valid && a.Valid && e.Valid && d.Valid {
		x.Access = &rules.AccessMatrix{Read: r.String, Add: a.String, Edit: e.String, Delete: d.String}
	}
	return x, nil
}

// LoadResource reads one resource and, for SQLite, its table rules.
func LoadResource(ctx context.Context, q Querier, siteID, name string) (Resource, error) {
	x, err := ScanResource(q.QueryRowContext(ctx, `SELECT `+ResourceColumns+` FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name).Scan)
	if err != nil {
		return x, err
	}
	if x.Kind == "sqlite" && (x.Access != nil || x.Rules != nil) {
		x.Tables, err = LoadTables(ctx, q, siteID, name)
	}
	return x, err
}

// LoadTables reads the per-table access rules for a resource.
func LoadTables(ctx context.Context, q Querier, siteID, name string) (map[string]rules.TableRule, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base,rules FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]rules.TableRule{}
	for rows.Next() {
		var n string
		var t rules.TableRule
		var rawRules []byte
		if err := rows.Scan(&n, &t.Matrix.Read, &t.Matrix.Add, &t.Matrix.Edit, &t.Matrix.Delete, &t.PresetBase, &rawRules); err != nil {
			return nil, err
		}
		t.Rules = rawRules
		out[n] = t
	}
	return out, rows.Err()
}
