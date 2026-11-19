package storage

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/repository/sqlkit"
	rules "github.com/vsriram/simple-host/host/rules/storage"
)

// Querier supports resource queries on a database or transaction.
type Querier interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Resource is the storage resource record shared with policy rules.
type Resource = rules.Resource

// ResourceColumns is the ordered list of columns read by ScanResource.
const ResourceColumns = `name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read IS NULL,rules`

// ScanResource scans the resource columns from a row.
func ScanResource(scan func(...any) error) (Resource, error) {
	var x Resource
	var rawRules []byte
	if err := scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode, &x.WriteMode, &x.BeforePresets, &rawRules); err != nil {
		return x, err
	}
	x.Rules = rawRules
	return x, nil
}

// LoadResource reads one resource and, for SQLite, its table rules.
func LoadResource(ctx context.Context, q Querier, siteID, name string) (Resource, error) {
	x, err := ScanResource(q.QueryRowContext(ctx, `SELECT `+ResourceColumns+` FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name).Scan)
	if err != nil {
		return x, err
	}
	// Table rules apply only beside resource rules (EffectiveRules).
	if x.Kind == "sqlite" && x.Rules != nil {
		x.Tables, err = LoadTables(ctx, q, siteID, name)
	}
	return x, err
}

// LoadTables reads the per-table rules for a resource.
func LoadTables(ctx context.Context, q Querier, siteID, name string) (map[string]rules.TableRule, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name,rules FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, siteID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]rules.TableRule{}
	for rows.Next() {
		var n string
		var t rules.TableRule
		var rawRules []byte
		if err := rows.Scan(&n, &rawRules); err != nil {
			return nil, err
		}
		t.Rules = rawRules
		out[n] = t
	}
	return out, rows.Err()
}
