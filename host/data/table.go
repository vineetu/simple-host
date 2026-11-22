package data

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// openTableConn opens the per-site database and translates runtime errors.
// Write callers pass their bounded lock context so quota measurement and the
// connection share the same deadline as the rest of the mutation.
func (s *Service) OpenTableConn(ctx context.Context, c Call, write bool) (*sqlite3.Conn, func(), error) {
	timeout := s.Deps.SQLiteTimeout
	var remaining func() (int64, error)
	if write {
		timeout = s.Deps.WriteTimeout
		remaining = func() (int64, error) {
			u, err := s.MeasureSiteStorageAdmitted(ctx, c.SiteID, c.RuntimeDir)
			return SiteLimitBytes(s.Deps.SiteLimit, s.Deps.CombinedPool) - s.BudgetUsed(u), err
		}
	}
	conn, done, err := runtime.OpenTableConn(ctx, s.Deps.SQLitePath, write, timeout, s.Deps.SQLiteMaxMemory, remaining)
	var storageErr *Error
	if errors.As(err, &storageErr) && storageErr.Code == "storage_busy" {
		return nil, nil, storageErr
	}
	if e := SQLiteFailure(err); e != err {
		return nil, nil, e
	}
	switch err {
	case nil:
		return conn, done, nil
	case runtime.ErrSQLiteBusy:
		return nil, nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
	case runtime.ErrUnknownTable:
		return nil, nil, &Error{Code: "invalid_rows", Message: "unknown table"}
	case runtime.ErrTableFull:
		return nil, nil, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	default:
		return nil, nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
}

// rowMetadata is a table's checked columns, rowid expression and whether new
// rows wait for approval.
type RowMetadata struct {
	Cols  map[string]string
	Order []string
	RowID string
	Hold  bool
}

// declaredColumn resolves SQLite's case-insensitive names to their schema spelling.
func DeclaredColumn(cols map[string]string, name string) string {
	for column := range cols {
		if strings.EqualFold(column, name) {
			return column
		}
	}
	return ""
}

// tableMetadata applies the checks shared by table reads and writes.
func TableMetadata(conn *sqlite3.Conn, c Call, table string) (RowMetadata, error) {
	var m RowMetadata
	var err error
	if c.RulePlan != nil {
		m.Cols, m.Order, err = runtime.TableXInfo(conn, table)
	} else {
		m.Cols, m.Order, err = runtime.TableInfo(conn, table)
	}
	if err != nil {
		return m, SQLiteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	if !c.Owner && len(m.Cols) > 100 {
		return m, &Error{Code: "invalid_rows", Message: "too many columns"}
	}
	visitorColumn := DeclaredColumn(m.Cols, "visitor_id")
	writtenByColumn := DeclaredColumn(m.Cols, "written_by")
	if c.OwnerFor != "" {
		if m.Cols[visitorColumn] != "TEXT" {
			return m, &Error{Code: "visitor_id_required", Message: "adding for someone needs the visitor_id TEXT column; set the policy again so the server adds it"}
		}
		if m.Cols[writtenByColumn] != "TEXT" {
			return m, &Error{Code: "written_by_required", Message: "adding for someone needs the written_by TEXT column; set the policy again so the server adds it"}
		}
	}
	m.RowID, err = runtime.RowID(conn, table, m.Cols)
	if err != nil {
		return m, SQLiteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	approvalColumn := DeclaredColumn(m.Cols, "approved_at")
	if c.RulePlan != nil {
		m.Hold = c.RulePlan.ReferencesColumn("approved_at") && m.Cols[approvalColumn] == "TEXT"
	} else {
		// Owners bypass row rules but keep the same approval stamping.
		set, errs := ruleengine.DecodeSet(c.Resource.EffectiveRules(table))
		if len(errs) == 0 {
			_, hold, err := ruleengine.RequiredColumns(set, PlatformSchema(true), ruleengine.DefaultLimits())
			m.Hold = err == nil && hold && m.Cols[approvalColumn] == "TEXT"
		}
	}
	if m.Hold && m.Cols[approvalColumn] != "TEXT" {
		return m, &Error{Code: "approved_at_required", Message: "approve_first needs the approved_at TEXT column; set the policy again so the server adds it"}
	}
	return m, nil
}

func RowJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func RowReadAuthorizer(conn *sqlite3.Conn, owner bool) error {
	if owner {
		return conn.SetAuthorizer(nil)
	}
	return conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			return sqlite3.AUTH_DENY
		}
		switch a {
		case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_FUNCTION:
			n4 = strings.ToLower(n4)
			if n4 != "load_extension" && n4 != "readfile" && n4 != "writefile" {
				return sqlite3.AUTH_OK
			}
		}
		return sqlite3.AUTH_DENY
	})
}

func (s *Service) SQLiteStage(stage string) {
	if s.SQLiteHook != nil {
		s.SQLiteHook(stage)
	}
}
