package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sqlite3 "github.com/ncruces/go-sqlite3"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	"github.com/vsriram/simple-host/internal/storage"
	"io"
	"log"
	"regexp"
	"strings"
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
	Refusals     []*Error
}

// LegacySQL executes a single legacy SQLite request.
func (s *Service) LegacySQL(ctx context.Context, in LegacySQLInput) (result LegacySQLResult, resultErr error) {
	var refusals []*Error
	defer func() { result.Refusals = refusals }()
	c, mode := in.Call, in.Mode
	if !c.Owner && !c.Resource.LegacyVisitorSQL() {
		return LegacySQLResult{}, &Error{Code: "fixed_routes_required", Message: "pages use the table rows routes (table().list, get, add, edit, delete); SQL is for the owner's tools"}
	}
	if mode != "query" && mode != "execute" && mode != "schema" {
		return LegacySQLResult{}, &Error{Code: "not_found", Message: "not found"}
	}
	if mode == "schema" {
		if !c.Owner {
			return LegacySQLResult{}, &Error{Code: "forbidden", Message: "owner credential required"}
		}
	} else {
		c.Context = ctx
		action := "read"
		if mode == "execute" {
			action = "add"
		}
		filter, err := s.Access(&c, action)
		c.Filter = filter
		if err != nil {
			e := err.(*Error)
			if !e.Allowed {
				return LegacySQLResult{}, err
			}
			refusals = e.Refusals
		}
	}
	body, readErr := in.ReadBody()
	var req struct {
		SQL    string            `json:"sql"`
		Params []json.RawMessage `json:"params"`
	}
	var reader io.Reader = bytes.NewReader(body)
	if len(body) > 64<<10 {
		reader = io.MultiReader(bytes.NewReader(body[:64<<10]), legacyBodyTooLarge{})
	}
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&req); err != nil || dec.Decode(new(any)) != io.EOF {
		if errors.Is(err, errLegacyBodyTooLarge) {
			return LegacySQLResult{}, &Error{Code: "sql_request", Message: "the request body is over 64 KB: send a shorter statement, or fewer or smaller params"}
		}
		return LegacySQLResult{}, &Error{Code: "sql_request", Message: `the body must be one JSON object: {"sql": "one statement", "params": [values for each ?]}`}
	}
	if readErr != nil {
		return LegacySQLResult{}, &Error{Code: "sql_request", Message: `the body must be one JSON object: {"sql": "one statement", "params": [values for each ?]}`}
	}
	if e := ValidateLegacySQL(req.SQL, req.Params); e != nil {
		return LegacySQLResult{}, e
	}
	// written_by (SIM-358) is a server-owned column on the structured
	// table-rows path (stampInsertValues/TableEdit); the owner's raw SQL
	// console is a separate, pre-existing, unrestricted door that can
	// still set visitor_id (documented, long-standing behaviour, left as
	// is) but must not be able to forge written_by too. Cheap and
	// deliberately coarse: any INSERT/UPDATE/DELETE naming the identifier
	// anywhere is refused, rather than parsing the statement to see if it
	// is really a write to that column. schema is refused the same way
	// (review round 2): a CREATE TRIGGER/VIEW or ALTER body naming
	// written_by could otherwise set or clear it as a side effect of an
	// ordinary row-route insert, which runs with no authorizer for the
	// owner (runTableWrite) and so cannot itself catch a trigger's write.
	if (mode == "execute" || mode == "schema") && writtenByColumnRE.MatchString(req.SQL) {
		return LegacySQLResult{}, &Error{Code: "server_column", Message: "written_by is set by the server; raw SQL may not reference it, including inside a trigger or view"}
	}
	sql, params := req.SQL, req.Params
	var unlock func()
	if mode != "query" {
		var err error
		ctx, unlock, err = s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
		if err != nil {
			return LegacySQLResult{}, err
		}
		defer unlock()
	}

	dbPath := s.deps.SQLitePath
	var usage Usage
	var currentDBBytes int64
	newDB := false
	successful := false
	if mode != "query" {
		var e error
		usage, e = s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
		if e != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
		currentDBBytes, newDB, e = storage.LegacySQLiteSize(dbPath)
		if e != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
		if currentDBBytes == 0 && s.BudgetUsed(usage)+8192 > SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) {
			return LegacySQLResult{}, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		}
		defer func() {
			if newDB && !successful {
				storage.RemoveNewSQLite(dbPath)
			}
		}()
		if err := storage.EnsureSQLiteDirectory(dbPath); err != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	uri := dbPath
	if mode == "query" {
		var err error
		uri, err = storage.LegacySQLiteQueryURI(dbPath)
		if err != nil {
			return LegacySQLResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	resultErr = s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, a *Admission) error {
		var err error
		result, err = func() (LegacySQLResult, error) {
			timeout := s.deps.SQLiteTimeout
			if !c.Owner {
				timeout = s.deps.SQLiteTimeout
				if mode != "query" {
					timeout = s.deps.WriteTimeout
				}
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			limit := SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
			writeBytes := min(limit, currentDBBytes+limit-s.BudgetUsed(usage))
			conn, err := storage.OpenLegacySQLite(ctx, uri, s.deps.SQLiteMaxMemory, writeBytes, mode != "query")
			if conn != nil {
				defer closeSQLite(func() { defer conn.Close(); s.sqliteStage("close") })
			}
			if err != nil {
				return LegacySQLResult{}, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
			}
			s.sqliteStage("after_open")
			if mode != "query" {
				err = storage.SetLegacySQLiteLimit(conn, SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)-(s.BudgetUsed(usage)-currentDBBytes))
				if errors.Is(err, storage.ErrTableFull) {
					return LegacySQLResult{}, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
				}
				if err != nil {
					return LegacySQLResult{}, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
				}
			}
			var before map[string]bool
			// Every table that uses own (all of a legacy read-own database) keeps
			// the server-owned visitor_id column.
			ownSchema := mode == "schema" && (c.Resource.Read == "own" || c.Resource.Access != nil)
			if ownSchema {
				var e error
				before, e = storage.SQLTables(conn)
				if e != nil || conn.Exec("BEGIN IMMEDIATE") != nil {
					return LegacySQLResult{}, &Error{Code: "storage_busy", Message: "could not start the schema change because the database is busy; call once more in a second"}
				}
				defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
			}
			var authorizerState storage.LegacyAuthorizerState
			// denial keeps the first rule the authorizer applied, so a refused
			// statement can say which one; writeTable names the table a write
			// targets, for a foreign key failure.
			var denial *sqlRefusal
			deny := func(why *sqlRefusal) sqlite3.AuthorizerReturnCode {
				if denial == nil {
					denial = why
				}
				return sqlite3.AUTH_DENY
			}
			err = storage.LegacyAuthorizer(conn, mode, c.Resource.Read, c.VisitorID, c.Owner, &authorizerState, deny, sqlAuthorizerDenial, sqlVisitorReadDenial)
			if err != nil {
				return LegacySQLResult{}, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
			}
			fail := func(err error) error {
				if errors.Is(err, sqlite3.NOMEM) {
					return sqliteFailure(err)
				}
				return sqlFailure(ctx, conn, err, c.Owner, sql, authorizerState.WriteTable, authorizerState.WriteAction, timeout)
			}
			stmt, tail, err := storage.PrepareLegacyStatement(conn, sql)
			if err != nil || stmt == nil {
				switch {
				case denial != nil:
					return LegacySQLResult{}, denial
				case err != nil:
					return LegacySQLResult{}, fail(err)
				default:
					return LegacySQLResult{}, &Error{Code: "sql_request", Message: "sql holds no statement (only blank space or comments): send one SQL statement"}
				}
			}
			defer storage.CloseLegacyStatement(stmt)
			s.sqliteStage("after_prepare")
			if second := sqlSecondStatement(tail); second != "" {
				return LegacySQLResult{}, sqlMultipleStatements(second)
			}
			bindCount, readOnly := storage.LegacyStatementInfo(stmt)
			if len(params) != bindCount {
				return LegacySQLResult{}, sqlParamCount(bindCount, len(params))
			}
			// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
			// a read-only no-op, so the authorizer never sees a schema change (tables
			// and views still report theirs; an existing index compiles as a write and
			// stays refused); the owner's setup is meant to be rerunnable, so that
			// no-op passes the schema route.
			noopCreate := mode == "schema" && !authorizerState.DidSchema && !authorizerState.DidWrite && readOnly && storage.CreateIfNotExists(sql)
			if refusal := sqlRouteMismatch(mode, sql, readOnly, authorizerState.DidWrite, authorizerState.DidSchema, noopCreate); refusal != nil {
				return LegacySQLResult{}, refusal
			}
			if i := storage.BindLegacyStatement(stmt, params); i >= 0 {
				return LegacySQLResult{}, &Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] could not be used: each param must be a string, a number that fits in 64 bits, true, false or null", i)}
			}
			if mode == "query" {
				result, err := storage.LegacyQueryResult(stmt, in.ResultLimit)
				switch err {
				case storage.ErrLegacyQueryRows:
					return LegacySQLResult{}, &Error{Code: "result_too_large", Message: "query returned more than 500 rows"}
				case storage.ErrLegacyQuerySize:
					return LegacySQLResult{}, &Error{Code: "result_too_large", Message: "query result is too large"}
				case nil:
				default:
					return LegacySQLResult{}, fail(err)
				}
				s.sqliteStage("after_step")
				return LegacySQLResult{Query: true, Columns: result.Columns, Rows: result.Rows}, nil
			}
			// Non-schema writes use SQLite's implicit commit in Exec. Record success
			// immediately afterwards, before any checkpoint or cleanup can panic.
			if !ownSchema {
				s.sqliteStage("before_commit")
			}
			err = storage.ExecuteLegacyStatement(stmt)
			if err != nil {
				if errors.Is(err, sqlite3.FULL) {
					return LegacySQLResult{}, &Error{Code: "sqlite_full", Message: "SQLite cannot allocate more storage or row IDs"}
				}
				return LegacySQLResult{}, fail(err)
			}
			result = LegacySQLResult{Changes: conn.Changes(), LastInsertID: conn.LastInsertRowID(), Schema: mode == "schema"}
			commitSucceeded := func() {
				a.Committed()
				successful = true
			}
			if !ownSchema {
				commitSucceeded()
			}
			defer func() {
				if successful {
					s.deps.MarkChanged()
					if (authorizerState.DidWrite || authorizerState.DidSchema) && s.deps.Committed != nil {
						s.deps.Committed(ctx)
					}
				}
			}()
			s.sqliteStage("after_step")
			var notes []string
			if ownSchema {
				if err = storage.CloseLegacyStatement(stmt); err == nil {
					err = conn.SetAuthorizer(nil)
				}
				// A table with its own preset must not lose it to a rename: the new
				// name would fall back to the database's preset, which may be wider.
				var after map[string]bool
				if err == nil && c.Resource.Access != nil {
					if after, err = storage.SQLTables(conn); err == nil {
						renamed, added := storage.SchemaTableChanges(before, after, func(t string) bool {
							_, ruled := c.Resource.Tables[strings.ToLower(t)]
							return ruled
						})
						if renamed && len(added) > 0 {
							return LegacySQLResult{}, &Error{Code: "table_preset_rename", Message: "this table has its own preset; give the new name its preset first (storage_set_resource tables), then rename"}
						}
						for _, t := range added {
							m := c.Resource.TableMatrix(t)
							if _, ruled := c.Resource.Tables[strings.ToLower(t)]; ruled {
								notes = append(notes, "new table "+t+" uses the preset saved for it ("+rules.PresetName(m)+")")
							} else if m != (rules.AccessMatrix{Read: "owner", Add: "owner", Edit: "owner", Delete: "owner"}) {
								notes = append(notes, "new table "+t+" uses the database's preset ("+rules.PresetName(m)+"); give it its own with storage_set_resource tables if it should be narrower")
							}
						}
					}
				}
				if err == nil {
					err = storage.EnsureOwnTables(conn, before, c.Resource.OwnTables())
				}
				if err == nil {
					err = storage.EnsureHoldTables(conn, c.Resource.Holds)
				}
				if err != nil {
					return LegacySQLResult{}, &Error{Code: "visitor_id_required", Message: "every table whose preset uses own or lets visitors add must keep its visitor_id TEXT column; only new tables receive it automatically, so do not drop or rename it"}
				}
				result.Changes = conn.Changes()
				result.Notes = notes
				s.sqliteStage("before_commit")
				if err = conn.Exec("COMMIT"); err != nil {
					return LegacySQLResult{}, fail(err)
				}
				commitSucceeded()
			}
			s.sqliteStage("after_commit")
			if err = storage.CloseLegacyStatement(stmt); err != nil {
				return LegacySQLResult{}, &Error{Code: "internal_error", Message: "SQL commit failed"}
			}
			// Committed already: a failed checkpoint must not read as a failed
			// change (a retried INSERT would be a duplicate).
			if err = storage.CheckpointLegacySQLite(conn); err != nil {
				log.Printf("storage: site %s %s: checkpoint after commit: %v", c.SiteID, c.ResourceName, err)
			}
			return result, nil
		}()
		return err
	})
	return result, resultErr
}

var errLegacyBodyTooLarge = errors.New("legacy SQL body too large")

type legacyBodyTooLarge struct{}

func (legacyBodyTooLarge) Read([]byte) (int, error) { return 0, errLegacyBodyTooLarge }
