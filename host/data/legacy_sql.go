package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data/rules"
	"github.com/vsriram/simple-host/host/storage"
)

// writtenByColumnRE matches the written_by identifier anywhere in a raw SQL
// statement, quoted or not.
var writtenByColumnRE = regexp.MustCompile(`(?i)\bwritten_by\b`)

// LegacySQLInput contains the request and a deferred body reader.
type LegacySQLInput struct {
	Call        Call
	Mode        string
	ReadBody    func() ([]byte, error)
	ResultLimit int
}

// LegacySQLResult contains the fields returned by a legacy SQL operation.
type LegacySQLResult struct {
	Query        bool
	Schema       bool
	Columns      []string
	Rows         [][]any
	Changes      int64
	LastInsertID int64
	Notes        []string
}

var legacySQLModes = map[string]bool{"query": true, "execute": true, "schema": true}

// LegacySQL executes a single legacy SQLite request.
func (s *Service) LegacySQL(ctx context.Context, in LegacySQLInput) (LegacySQLResult, error) {
	if !in.Call.Owner {
		return LegacySQLResult{}, &Error{Code: "fixed_routes_required", Message: "pages use the table rows routes (table().list, get, add, edit, delete); SQL is for the owner's tools"}
	}
	if !legacySQLModes[in.Mode] {
		return LegacySQLResult{}, &Error{Code: "not_found", Message: "not found"}
	}
	req, err := readLegacySQLRequest(in.ReadBody)
	if err != nil {
		return LegacySQLResult{}, err
	}
	if e := ValidateLegacySQL(req.SQL, req.Params); e != nil {
		return LegacySQLResult{}, e
	}
	// written_by is server-owned on the table-rows path. The owner's console
	// may still set visitor_id but must not forge written_by, so any change
	// naming it is refused, schema changes too: a trigger or view naming it
	// could set it during a row-route insert, which runs with no authorizer
	// for the owner (runTableWrite).
	if in.Mode != "query" && writtenByColumnRE.MatchString(req.SQL) {
		return LegacySQLResult{}, &Error{Code: "server_column", Message: "written_by is set by the server; raw SQL may not reference it, including inside a trigger or view"}
	}
	dbPath := s.deps.SQLitePath
	uri, room := dbPath, SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	committed := false
	if in.Mode == "query" {
		if uri, err = storage.LegacySQLiteQueryURI(dbPath); err != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	} else {
		var unlock func()
		ctx, unlock, err = s.writeLock(ctx, in.Call.OwnerID+"/"+in.Call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
		if err != nil {
			return LegacySQLResult{}, err
		}
		defer unlock()
		var newDB bool
		if room, newDB, err = s.legacySQLRoom(ctx, in.Call, dbPath); err != nil {
			return LegacySQLResult{}, err
		}
		if newDB {
			defer func() {
				if !committed {
					storage.RemoveNewSQLite(dbPath)
				}
			}()
		}
		if err := storage.EnsureSQLiteDirectory(dbPath); err != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	// A failure clears the result; a panic after the commit keeps it, so a
	// committed change is not reported as lost.
	var result LegacySQLResult
	err = s.deps.SiteSlots.Admit(ctx, in.Call.SiteID, func(ctx context.Context, a *Admission) error {
		defer func() { committed = a.committed }()
		err := s.runLegacySQL(ctx, a, in, req, uri, room, &result)
		if err != nil {
			result = LegacySQLResult{}
		}
		return err
	})
	return result, err
}

var errLegacyBodyTooLarge = errors.New("legacy SQL body too large")

type legacyBodyTooLarge struct{}

func (legacyBodyTooLarge) Read([]byte) (int, error) { return 0, errLegacyBodyTooLarge }

type legacySQLRequest struct {
	SQL    string            `json:"sql"`
	Params []json.RawMessage `json:"params"`
}

// readLegacySQLRequest reads the body as exactly one JSON object of at most 64 KB.
func readLegacySQLRequest(read func() ([]byte, error)) (legacySQLRequest, error) {
	body, readErr := read()
	var req legacySQLRequest
	var reader io.Reader = bytes.NewReader(body)
	if len(body) > 64<<10 {
		reader = io.MultiReader(bytes.NewReader(body[:64<<10]), legacyBodyTooLarge{})
	}
	dec := json.NewDecoder(reader)
	err := dec.Decode(&req)
	if errors.Is(err, errLegacyBodyTooLarge) {
		return req, &Error{Code: "sql_request", Message: "the request body is over 64 KB: send a shorter statement, or fewer or smaller params"}
	}
	if err != nil || dec.Decode(new(any)) != io.EOF || readErr != nil {
		return req, &Error{Code: "sql_request", Message: `the body must be one JSON object: {"sql": "one statement", "params": [values for each ?]}`}
	}
	return req, nil
}

// legacySQLRoom returns the storage a change may use (the site's remaining
// room plus the database's current size) and whether the database is new,
// refusing to create one when the site has no room for its first pages.
func (s *Service) legacySQLRoom(ctx context.Context, c Call, dbPath string) (int64, bool, error) {
	usage, err := s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
	if err != nil {
		var storageErr *Error
		if errors.As(err, &storageErr) && storageErr.Code == "storage_busy" {
			return 0, false, storageErr
		}
		return 0, false, &Error{Code: "internal_error", Message: "internal server error"}
	}
	dbBytes, newDB, err := storage.LegacySQLiteSize(dbPath)
	if err != nil {
		return 0, false, &Error{Code: "internal_error", Message: "internal server error"}
	}
	limit := SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	if dbBytes == 0 && s.BudgetUsed(usage)+8192 > limit {
		return 0, false, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	}
	return dbBytes + limit - s.BudgetUsed(usage), newDB, nil
}

// runLegacySQL opens the database, compiles and checks the statement, then
// runs it as a query or a change. A schema change runs in its own
// transaction. The result is written to out as soon as it exists, so a panic
// after the commit still reports it.
func (s *Service) runLegacySQL(ctx context.Context, a *Admission, in LegacySQLInput, req legacySQLRequest, uri string, room int64, out *LegacySQLResult) error {
	ctx, cancel := context.WithTimeout(ctx, s.deps.SQLiteTimeout)
	defer cancel()
	limit := SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	conn, err := storage.OpenLegacySQLite(ctx, uri, s.deps.SQLiteMaxMemory, min(limit, room), in.Mode != "query")
	if conn != nil {
		defer closeSQLite(func() { defer conn.Close(); s.sqliteStage("close") })
	}
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	s.sqliteStage("after_open")
	if in.Mode != "query" {
		err := storage.SetLegacySQLiteLimit(conn, room)
		if errors.Is(err, storage.ErrTableFull) {
			return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		}
		if err != nil {
			return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
		}
	}
	var before map[string]bool
	if in.Mode == "schema" {
		// A schema change keeps every column the rules need (ensureSchemaRules).
		if before, err = storage.SQLTables(conn); err != nil || conn.Exec("BEGIN IMMEDIATE") != nil {
			return &Error{Code: "storage_busy", Message: "could not start the schema change because the database is busy; call once more in a second"}
		}
		defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
	}
	var authz storage.LegacyAuthorizerState
	fail := func(err error) error {
		if errors.Is(err, sqlite3.NOMEM) {
			return sqliteFailure(err)
		}
		return sqlFailure(ctx, conn, err, in.Call.Owner, req.SQL, authz.WriteTable, authz.WriteAction, s.deps.SQLiteTimeout)
	}
	stmt, err := s.prepareLegacySQL(conn, in.Mode, req, &authz, fail)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if in.Mode == "query" {
		rows, err := storage.LegacyQueryResult(stmt, in.ResultLimit)
		switch err {
		case nil:
		case storage.ErrLegacyQueryRows:
			return &Error{Code: "result_too_large", Message: "query returned more than 500 rows"}
		case storage.ErrLegacyQuerySize:
			return &Error{Code: "result_too_large", Message: "query result is too large"}
		default:
			return fail(err)
		}
		s.sqliteStage("after_step")
		*out = LegacySQLResult{Query: true, Columns: rows.Columns, Rows: rows.Rows}
		return nil
	}
	return s.changeLegacySQL(ctx, conn, stmt, a, in.Call, in.Mode == "schema", before, &authz, fail, out)
}

// prepareLegacySQL compiles the statement under the route's authorizer and
// refuses one that is not alone, lacks a value for a placeholder or belongs
// to another route, then binds the params. A refused statement names the
// first rule the authorizer applied. The statement is closed here unless it
// is returned without error, even when a step panics.
func (s *Service) prepareLegacySQL(conn *sqlite3.Conn, mode string, req legacySQLRequest, authz *storage.LegacyAuthorizerState, fail func(error) error) (*sqlite3.Stmt, error) {
	var denial *sqlRefusal
	deny := func(why *sqlRefusal) sqlite3.AuthorizerReturnCode {
		if denial == nil {
			denial = why
		}
		return sqlite3.AUTH_DENY
	}
	if err := storage.LegacyAuthorizer(conn, mode, authz, deny, sqlAuthorizerDenial); err != nil {
		return nil, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	stmt, tail, err := conn.Prepare(req.SQL)
	switch {
	case err == nil && stmt != nil:
	case denial != nil:
		return nil, denial
	case err != nil:
		return nil, fail(err)
	default:
		return nil, &Error{Code: "sql_request", Message: "sql holds no statement (only blank space or comments): send one SQL statement"}
	}
	ok := false
	defer func() {
		if !ok {
			stmt.Close()
		}
	}()
	s.sqliteStage("after_prepare")
	if second := rules.SQLSecondStatement(tail); second != "" {
		return nil, sqlRefuse("sql_multiple_statements", rules.SQLMultipleStatements(second))
	}
	bindCount, readOnly := stmt.BindCount(), stmt.ReadOnly()
	if len(req.Params) != bindCount {
		return nil, sqlRefuse("sql_param_count", rules.SQLParamCount(bindCount, len(req.Params)))
	}
	// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
	// a read-only no-op, so the authorizer never sees a schema change (tables
	// and views still report theirs; an existing index compiles as a write and
	// stays refused); the owner's setup is meant to be rerunnable, so that
	// no-op passes the schema route.
	noopCreate := mode == "schema" && !authz.DidSchema && !authz.DidWrite && readOnly && storage.CreateIfNotExists(req.SQL)
	if refusal := sqlRouteMismatch(mode, req.SQL, readOnly, authz.DidWrite, authz.DidSchema, noopCreate); refusal != nil {
		return nil, refusal
	}
	if i := storage.BindLegacyStatement(stmt, req.Params); i >= 0 {
		return nil, &Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] could not be used: each param must be a string, a number that fits in 64 bits, true, false or null", i)}
	}
	ok = true
	return stmt, nil
}

// changeLegacySQL runs an execute or schema statement and commits it. An
// execute commits inside Exec (SQLite's implicit transaction), so success is
// recorded straight afterwards, before any checkpoint or cleanup can panic; a
// schema change commits only after finishSchema accepts it. Once committed,
// the site is told even if a later step fails.
func (s *Service) changeLegacySQL(ctx context.Context, conn *sqlite3.Conn, stmt *sqlite3.Stmt, a *Admission, c Call, schema bool, before map[string]bool, authz *storage.LegacyAuthorizerState, fail func(error) error, out *LegacySQLResult) error {
	if !schema {
		s.sqliteStage("before_commit")
	}
	if err := stmt.Exec(); err != nil {
		if errors.Is(err, sqlite3.FULL) {
			return &Error{Code: "sqlite_full", Message: "SQLite cannot allocate more storage or row IDs"}
		}
		return fail(err)
	}
	*out = LegacySQLResult{Changes: conn.Changes(), LastInsertID: conn.LastInsertRowID(), Schema: schema}
	if !schema {
		a.Committed()
	}
	defer func() {
		if !a.committed {
			return
		}
		s.deps.MarkChanged()
		if (authz.DidWrite || authz.DidSchema) && s.deps.Committed != nil {
			s.deps.Committed(ctx)
		}
	}()
	s.sqliteStage("after_step")
	if schema {
		notes, err := s.finishSchema(conn, stmt, c, before)
		if err != nil {
			return err
		}
		out.Changes, out.Notes = conn.Changes(), notes
		s.sqliteStage("before_commit")
		if err := conn.Exec("COMMIT"); err != nil {
			return fail(err)
		}
		a.Committed()
	}
	s.sqliteStage("after_commit")
	if err := stmt.Close(); err != nil {
		return &Error{Code: "internal_error", Message: "SQL commit failed"}
	}
	// Committed already: a failed checkpoint must not read as a failed
	// change (a retried INSERT would be a duplicate).
	if err := storage.CheckpointLegacySQLite(conn); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", c.SiteID, c.ResourceName, err)
	}
	return nil
}

// finishSchema checks a schema change before it commits and says which rules
// each new table gets. A table with its own rules must not lose them to a
// rename: the new name would fall back to the database's rules, which may be
// wider. Author-tracked rules tables keep their columns (ensureSchemaRules).
func (s *Service) finishSchema(conn *sqlite3.Conn, stmt *sqlite3.Stmt, c Call, before map[string]bool) ([]string, error) {
	columnsLost := &Error{Code: "visitor_id_required", Message: "every author-tracked rules table must keep its visitor_id TEXT column"}
	if stmt.Close() != nil || conn.SetAuthorizer(nil) != nil {
		return nil, columnsLost
	}
	after, err := storage.SQLTables(conn)
	if err != nil {
		return nil, columnsLost
	}
	res := c.Resource
	hasOwn := func(t string) bool { _, own := res.Tables[strings.ToLower(t)]; return own }
	renamed, added := storage.SchemaTableChanges(before, after, hasOwn)
	var notes []string
	for _, t := range added {
		switch {
		case renamed && res.Rules != nil && !hasOwn(t):
			return nil, &Error{Code: "table_preset_rename", Message: "this table has its own rules; give the new name its rules first (storage_set_resource tables), then rename"}
		case hasOwn(t) && res.Rules != nil:
			notes = append(notes, "new table "+t+" uses the rules saved for it")
		default:
			notes = append(notes, "new table "+t+" uses the database rules; give it its own with storage_set_resource tables if it should be narrower")
		}
	}
	ruleNotes, err := s.ensureSchemaRules(conn, c, before)
	if err != nil {
		return nil, columnsLost
	}
	return append(notes, ruleNotes...), nil
}
