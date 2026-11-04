package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sqlite3 "github.com/ncruces/go-sqlite3"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	"github.com/vsriram/simple-host/internal/storage"
	"log"
	"strings"
)

// LegacySQLAuthorization carries the decisions made before decoding the request.
type LegacySQLAuthorization struct {
	call              Call
	mode              string
	verified, allowed bool
	Error             *Error
}

// AuthorizeLegacySQL checks the legacy route and its access policy before decoding.
func (s *Service) AuthorizeLegacySQL(ctx context.Context, c Call, mode string) LegacySQLAuthorization {
	out := LegacySQLAuthorization{call: c, mode: mode, verified: true}
	if !c.Owner && !c.Resource.LegacyVisitorSQL() {
		out.Error = &Error{Code: "fixed_routes_required", Message: "pages use the table rows routes (table().list, get, add, edit, delete); SQL is for the owner's tools"}
		return out
	}
	if mode != "query" && mode != "execute" && mode != "schema" {
		out.Error = &Error{Code: "not_found", Message: "not found"}
		return out
	}
	if mode == "schema" {
		if !c.Owner {
			out.Error = &Error{Code: "forbidden", Message: "owner credential required"}
			return out
		}
	} else {
		c.Context = ctx
		action := "read"
		if mode == "execute" {
			action = "add"
		}
		filter, err := s.Access(&c, action)
		c.Filter = filter
		out.call = c
		if err != nil {
			out.Error = err.(*Error)
			out.allowed = out.Error.Allowed
			return out
		}
	}
	out.allowed = true
	return out
}

// Allowed reports whether the authorized request may continue.
func (a LegacySQLAuthorization) Allowed() bool { return a.verified && a.allowed }

// LegacySQLInput contains one validated legacy SQL request.
type LegacySQLInput struct {
	Authorization LegacySQLAuthorization
	SQL           string
	Params        []json.RawMessage
	ResultLimit   int
}

// LegacySQL executes a single legacy SQLite request.
func (s *Service) LegacySQL(ctx context.Context, in LegacySQLInput) (map[string]any, error) {
	if !in.Authorization.Allowed() {
		if in.Authorization.Error != nil {
			return nil, in.Authorization.Error
		}
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	c, mode := in.Authorization.call, in.Authorization.mode
	var unlock func()
	if mode != "query" {
		var err error
		ctx, unlock, err = s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
		if err != nil {
			return nil, err
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
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
		currentDBBytes, newDB, e = storage.LegacySQLiteSize(dbPath)
		if e != nil {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
		if currentDBBytes == 0 && s.BudgetUsed(usage)+8192 > SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) {
			return nil, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		}
		defer func() {
			if newDB && !successful {
				storage.RemoveNewSQLite(dbPath)
			}
		}()
		if err := storage.EnsureSQLiteDirectory(dbPath); err != nil {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	uri := dbPath
	if mode == "query" {
		var err error
		uri, err = storage.LegacySQLiteQueryURI(dbPath)
		if err != nil {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	release, err := s.deps.SQLiteGate.Acquire(ctx)
	if err != nil {
		return nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
	}
	defer release()
	timeout := s.deps.SQLiteTimeout
	if !c.Owner {
		timeout = s.deps.SQLiteTimeout
		if mode != "query" {
			timeout = s.deps.WriteTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := storage.OpenLegacySQLite(ctx, uri, s.deps.SQLiteMaxMemory, mode != "query")
	if conn != nil {
		defer conn.Close()
	}
	if err != nil {
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	if mode != "query" {
		err = storage.SetLegacySQLiteLimit(conn, SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)-(s.BudgetUsed(usage)-currentDBBytes))
		if errors.Is(err, storage.ErrTableFull) {
			return nil, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		}
		if err != nil {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
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
			return nil, &Error{Code: "storage_busy", Message: "could not start the schema change because the database is busy; call once more in a second"}
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
		return nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
	fail := func(err error) error {
		return sqlFailure(ctx, conn, err, c.Owner, in.SQL, authorizerState.WriteTable, authorizerState.WriteAction, timeout)
	}
	stmt, tail, err := conn.Prepare(in.SQL)
	if err != nil || stmt == nil {
		switch {
		case denial != nil:
			return nil, denial
		case err != nil:
			return nil, fail(err)
		default:
			return nil, &Error{Code: "sql_request", Message: "sql holds no statement (only blank space or comments): send one SQL statement"}
		}
	}
	defer stmt.Close()
	if second := sqlSecondStatement(tail); second != "" {
		return nil, sqlMultipleStatements(second)
	}
	if len(in.Params) != stmt.BindCount() {
		return nil, sqlParamCount(stmt.BindCount(), len(in.Params))
	}
	// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
	// a read-only no-op, so the authorizer never sees a schema change (tables
	// and views still report theirs; an existing index compiles as a write and
	// stays refused); the owner's setup is meant to be rerunnable, so that
	// no-op passes the schema route.
	noopCreate := mode == "schema" && !authorizerState.DidSchema && !authorizerState.DidWrite && stmt.ReadOnly() && storage.CreateIfNotExists(in.SQL)
	if refusal := sqlRouteMismatch(mode, in.SQL, stmt.ReadOnly(), authorizerState.DidWrite, authorizerState.DidSchema, noopCreate); refusal != nil {
		return nil, refusal
	}
	for i, raw := range in.Params {
		if err = storage.BindStorageParam(stmt, i+1, raw); err != nil {
			return nil, &Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] could not be used: each param must be a string, a number that fits in 64 bits, true, false or null", i)}
		}
	}
	if mode == "query" {
		result, err := storage.LegacyQueryResult(stmt, in.ResultLimit)
		switch err {
		case storage.ErrLegacyQueryRows:
			return nil, &Error{Code: "result_too_large", Message: "query returned more than 500 rows"}
		case storage.ErrLegacyQuerySize:
			return nil, &Error{Code: "result_too_large", Message: "query result is too large"}
		case nil:
		default:
			return nil, fail(err)
		}
		return result, nil
	}
	err = stmt.Exec()
	if err != nil {
		if errors.Is(err, sqlite3.FULL) {
			return nil, &Error{Code: "sqlite_full", Message: "SQLite cannot allocate more storage or row IDs"}
		}
		return nil, fail(err)
	}
	var notes []string
	if ownSchema {
		if err = stmt.Close(); err == nil {
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
					return nil, &Error{Code: "table_preset_rename", Message: "this table has its own preset; give the new name its preset first (storage_set_resource tables), then rename"}
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
			return nil, &Error{Code: "visitor_id_required", Message: "every table whose preset uses own or lets visitors add must keep its visitor_id TEXT column; only new tables receive it automatically, so do not drop or rename it"}
		}
		if err = conn.Exec("COMMIT"); err != nil {
			return nil, fail(err)
		}
	}
	if authorizerState.DidWrite || authorizerState.DidSchema {
		s.deps.Committed(ctx)
	}
	successful = true
	if err = stmt.Close(); err != nil {
		return nil, &Error{Code: "internal_error", Message: "SQL commit failed"}
	}
	// Committed already: a failed checkpoint must not read as a failed
	// change (a retried INSERT would be a duplicate).
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", c.SiteID, c.ResourceName, err)
	}
	s.deps.MarkChanged()
	unlock()
	if mode == "schema" {
		resp := map[string]any{"changes": conn.Changes()}
		if len(notes) > 0 {
			resp["notes"] = notes
		}
		return resp, nil
	} else {
		return map[string]any{"changes": conn.Changes(), "last_insert_id": conn.LastInsertRowID()}, nil
	}
}
