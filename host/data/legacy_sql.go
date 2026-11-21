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
	"github.com/vsriram/simple-host/host/storage"
)

// writtenByColumnRE matches the written_by identifier anywhere in a raw SQL
// statement, quoted or not (SIM-358 review fix).
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

// legacySQLRun is one owner SQL statement on its way through the database:
// the request, the storage budget measured for a change, and the open
// connection with its authorizer's findings.
type legacySQLRun struct {
	s           *Service
	c           Call
	mode, sql   string
	params      []json.RawMessage
	resultLimit int

	usage     Usage
	dbBytes   int64
	newDB     bool
	committed bool

	ctx    context.Context
	conn   *sqlite3.Conn
	authz  storage.LegacyAuthorizerState
	before map[string]bool
	result LegacySQLResult
}

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
	// written_by (SIM-358) is server-owned on the table-rows path. The
	// owner's console may still set visitor_id (long-standing) but must not
	// forge written_by, so any change naming it is refused, schema changes
	// too: a trigger or view naming it could set it during a row-route
	// insert, which runs with no authorizer for the owner (runTableWrite).
	if in.Mode != "query" && writtenByColumnRE.MatchString(req.SQL) {
		return LegacySQLResult{}, &Error{Code: "server_column", Message: "written_by is set by the server; raw SQL may not reference it, including inside a trigger or view"}
	}
	r := &legacySQLRun{s: s, c: in.Call, mode: in.Mode, sql: req.SQL, params: req.Params, resultLimit: in.ResultLimit}
	dbPath := s.deps.SQLitePath
	if in.Mode == "query" {
		uri, err := storage.LegacySQLiteQueryURI(dbPath)
		if err != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
		return r.admit(ctx, uri)
	}
	ctx, unlock, err := s.writeLock(ctx, r.c.OwnerID+"/"+r.c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return LegacySQLResult{}, err
	}
	defer unlock()
	if err := r.measure(ctx, dbPath); err != nil {
		return LegacySQLResult{}, err
	}
	if r.newDB {
		defer func() {
			if !r.committed {
				storage.RemoveNewSQLite(dbPath)
			}
		}()
	}
	if err := storage.EnsureSQLiteDirectory(dbPath); err != nil {
		return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}
	return r.admit(ctx, dbPath)
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

// measure records the site's storage use and database size before a change,
// refusing to create a database when the site has no room for its first pages.
func (r *legacySQLRun) measure(ctx context.Context, dbPath string) error {
	usage, err := r.s.MeasureSiteStorage(ctx, r.c.SiteID, r.c.RuntimeDir)
	if err != nil {
		var storageErr *Error
		if errors.As(err, &storageErr) && storageErr.Code == "storage_busy" {
			return storageErr
		}
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	r.usage = usage
	if r.dbBytes, r.newDB, err = storage.LegacySQLiteSize(dbPath); err != nil {
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if r.dbBytes == 0 && r.s.BudgetUsed(usage)+8192 > SiteLimitBytes(r.s.deps.SiteLimit, r.s.deps.CombinedPool) {
		return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	}
	return nil
}

// admit runs the statement inside the site's admission. A failure clears the
// result; a panic after the commit keeps it, so a committed change is not
// reported as lost.
func (r *legacySQLRun) admit(ctx context.Context, uri string) (LegacySQLResult, error) {
	err := r.s.deps.SiteSlots.Admit(ctx, r.c.SiteID, func(ctx context.Context, a *Admission) error {
		err := r.execute(ctx, a, uri)
		if err != nil {
			r.result = LegacySQLResult{}
		}
		return err
	})
	return r.result, err
}

// execute opens the database, compiles and checks the statement, then runs
// it as a query or a change. A schema change runs in its own transaction.
func (r *legacySQLRun) execute(ctx context.Context, a *Admission, uri string) error {
	ctx, cancel := context.WithTimeout(ctx, r.s.deps.SQLiteTimeout)
	defer cancel()
	r.ctx = ctx
	limit := SiteLimitBytes(r.s.deps.SiteLimit, r.s.deps.CombinedPool)
	conn, err := storage.OpenLegacySQLite(ctx, uri, r.s.deps.SQLiteMaxMemory, min(limit, r.available()), r.mode != "query")
	if conn != nil {
		defer closeSQLite(func() { defer conn.Close(); r.s.sqliteStage("close") })
	}
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	r.conn = conn
	r.s.sqliteStage("after_open")
	if err := r.capChange(); err != nil {
		return err
	}
	if r.mode == "schema" {
		// A schema change keeps every column the rules need (ensureSchemaRules).
		if r.before, err = storage.SQLTables(conn); err != nil || conn.Exec("BEGIN IMMEDIATE") != nil {
			return &Error{Code: "storage_busy", Message: "could not start the schema change because the database is busy; call once more in a second"}
		}
		defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
	}
	stmt, tail, err := r.compile()
	if err != nil {
		return err
	}
	defer storage.CloseLegacyStatement(stmt)
	if err := r.check(stmt, tail); err != nil {
		return err
	}
	if r.mode == "query" {
		return r.query(stmt)
	}
	return r.change(stmt, a)
}

// available is the storage a change may use: the site's remaining room plus
// the database's current size.
func (r *legacySQLRun) available() int64 {
	return r.dbBytes + SiteLimitBytes(r.s.deps.SiteLimit, r.s.deps.CombinedPool) - r.s.BudgetUsed(r.usage)
}

// capChange caps the database's growth for a change at the available storage.
func (r *legacySQLRun) capChange() error {
	if r.mode == "query" {
		return nil
	}
	err := storage.SetLegacySQLiteLimit(r.conn, r.available())
	if errors.Is(err, storage.ErrTableFull) {
		return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	}
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	return nil
}

// compile prepares the statement under the route's authorizer. A refused
// statement names the first rule the authorizer applied.
func (r *legacySQLRun) compile() (*sqlite3.Stmt, string, error) {
	var denial *sqlRefusal
	deny := func(why *sqlRefusal) sqlite3.AuthorizerReturnCode {
		if denial == nil {
			denial = why
		}
		return sqlite3.AUTH_DENY
	}
	if err := storage.LegacyAuthorizer(r.conn, r.mode, &r.authz, deny, sqlAuthorizerDenial); err != nil {
		return nil, "", sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	stmt, tail, err := storage.PrepareLegacyStatement(r.conn, r.sql)
	switch {
	case err == nil && stmt != nil:
		return stmt, tail, nil
	case denial != nil:
		return nil, "", denial
	case err != nil:
		return nil, "", r.fail(err)
	}
	return nil, "", &Error{Code: "sql_request", Message: "sql holds no statement (only blank space or comments): send one SQL statement"}
}

// check refuses a compiled statement that is not alone, lacks a value for a
// placeholder or belongs to another route, then binds the params.
func (r *legacySQLRun) check(stmt *sqlite3.Stmt, tail string) error {
	r.s.sqliteStage("after_prepare")
	if second := sqlSecondStatement(tail); second != "" {
		return sqlMultipleStatements(second)
	}
	bindCount, readOnly := storage.LegacyStatementInfo(stmt)
	if len(r.params) != bindCount {
		return sqlParamCount(bindCount, len(r.params))
	}
	// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
	// a read-only no-op, so the authorizer never sees a schema change (tables
	// and views still report theirs; an existing index compiles as a write and
	// stays refused); the owner's setup is meant to be rerunnable, so that
	// no-op passes the schema route.
	noopCreate := r.mode == "schema" && !r.authz.DidSchema && !r.authz.DidWrite && readOnly && storage.CreateIfNotExists(r.sql)
	if refusal := sqlRouteMismatch(r.mode, r.sql, readOnly, r.authz.DidWrite, r.authz.DidSchema, noopCreate); refusal != nil {
		return refusal
	}
	if i := storage.BindLegacyStatement(stmt, r.params); i >= 0 {
		return &Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] could not be used: each param must be a string, a number that fits in 64 bits, true, false or null", i)}
	}
	return nil
}

// fail explains a SQLite error from compiling or running the statement.
func (r *legacySQLRun) fail(err error) error {
	if errors.Is(err, sqlite3.NOMEM) {
		return sqliteFailure(err)
	}
	return sqlFailure(r.ctx, r.conn, err, r.c.Owner, r.sql, r.authz.WriteTable, r.authz.WriteAction, r.s.deps.SQLiteTimeout)
}

// query reads the statement's rows, within the row and size caps.
func (r *legacySQLRun) query(stmt *sqlite3.Stmt) error {
	rows, err := storage.LegacyQueryResult(stmt, r.resultLimit)
	switch err {
	case nil:
	case storage.ErrLegacyQueryRows:
		return &Error{Code: "result_too_large", Message: "query returned more than 500 rows"}
	case storage.ErrLegacyQuerySize:
		return &Error{Code: "result_too_large", Message: "query result is too large"}
	default:
		return r.fail(err)
	}
	r.s.sqliteStage("after_step")
	r.result = LegacySQLResult{Query: true, Columns: rows.Columns, Rows: rows.Rows}
	return nil
}

// change runs an execute or schema statement and commits it. An execute
// commits inside Exec (SQLite's implicit transaction), so success is recorded
// straight afterwards, before any checkpoint or cleanup can panic; a schema
// change commits only after finishSchema accepts it.
func (r *legacySQLRun) change(stmt *sqlite3.Stmt, a *Admission) error {
	schema := r.mode == "schema"
	if !schema {
		r.s.sqliteStage("before_commit")
	}
	if err := storage.ExecuteLegacyStatement(stmt); err != nil {
		if errors.Is(err, sqlite3.FULL) {
			return &Error{Code: "sqlite_full", Message: "SQLite cannot allocate more storage or row IDs"}
		}
		return r.fail(err)
	}
	r.result = LegacySQLResult{Changes: r.conn.Changes(), LastInsertID: r.conn.LastInsertRowID(), Schema: schema}
	if !schema {
		r.markCommitted(a)
	}
	defer r.announce()
	r.s.sqliteStage("after_step")
	if schema {
		notes, err := r.finishSchema(stmt)
		if err != nil {
			return err
		}
		r.result.Changes, r.result.Notes = r.conn.Changes(), notes
		r.s.sqliteStage("before_commit")
		if err := r.conn.Exec("COMMIT"); err != nil {
			return r.fail(err)
		}
		r.markCommitted(a)
	}
	r.s.sqliteStage("after_commit")
	if err := storage.CloseLegacyStatement(stmt); err != nil {
		return &Error{Code: "internal_error", Message: "SQL commit failed"}
	}
	// Committed already: a failed checkpoint must not read as a failed
	// change (a retried INSERT would be a duplicate).
	if err := storage.CheckpointLegacySQLite(r.conn); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", r.c.SiteID, r.c.ResourceName, err)
	}
	return nil
}

func (r *legacySQLRun) markCommitted(a *Admission) {
	a.Committed()
	r.committed = true
}

// announce tells the site a committed change happened.
func (r *legacySQLRun) announce() {
	if !r.committed {
		return
	}
	r.s.deps.MarkChanged()
	if (r.authz.DidWrite || r.authz.DidSchema) && r.s.deps.Committed != nil {
		r.s.deps.Committed(r.ctx)
	}
}

// finishSchema checks a schema change before it commits and says which rules
// each new table gets. A table with its own rules must not lose them to a
// rename: the new name would fall back to the database's rules, which may be
// wider. Author-tracked rules tables keep their columns (ensureSchemaRules).
func (r *legacySQLRun) finishSchema(stmt *sqlite3.Stmt) ([]string, error) {
	columnsLost := &Error{Code: "visitor_id_required", Message: "every author-tracked rules table must keep its visitor_id TEXT column"}
	if storage.CloseLegacyStatement(stmt) != nil || r.conn.SetAuthorizer(nil) != nil {
		return nil, columnsLost
	}
	after, err := storage.SQLTables(r.conn)
	if err != nil {
		return nil, columnsLost
	}
	res := r.c.Resource
	hasOwn := func(t string) bool { _, own := res.Tables[strings.ToLower(t)]; return own }
	renamed, added := storage.SchemaTableChanges(r.before, after, hasOwn)
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
	ruleNotes, err := r.s.ensureSchemaRules(r.conn, r.c, r.before)
	if err != nil {
		return nil, columnsLost
	}
	return append(notes, ruleNotes...), nil
}
