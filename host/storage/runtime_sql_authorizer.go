package storage

import (
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// LegacyAuthorizerState records the statement actions seen during preparation.
type LegacyAuthorizerState struct {
	DidRead, DidWrite, DidSchema bool
	WriteTable                   string
	WriteAction                  sqlite3.AuthorizerActionCode
}

// legacyActionKind is what an authorizer action means to the owner's SQL tools.
type legacyActionKind uint8

const (
	legacyOther legacyActionKind = iota
	legacyForbidden
	legacyRead
	legacyWrite
	legacySchema
	legacyFunction
)

var legacyActions = map[sqlite3.AuthorizerActionCode]legacyActionKind{
	sqlite3.AUTH_ATTACH:              legacyForbidden,
	sqlite3.AUTH_DETACH:              legacyForbidden,
	sqlite3.AUTH_PRAGMA:              legacyForbidden,
	sqlite3.AUTH_CREATE_VTABLE:       legacyForbidden,
	sqlite3.AUTH_DROP_VTABLE:         legacyForbidden,
	sqlite3.AUTH_CREATE_TEMP_TABLE:   legacyForbidden,
	sqlite3.AUTH_CREATE_TEMP_INDEX:   legacyForbidden,
	sqlite3.AUTH_CREATE_TEMP_TRIGGER: legacyForbidden,
	sqlite3.AUTH_CREATE_TEMP_VIEW:    legacyForbidden,
	sqlite3.AUTH_DROP_TEMP_TABLE:     legacyForbidden,
	sqlite3.AUTH_DROP_TEMP_INDEX:     legacyForbidden,
	sqlite3.AUTH_DROP_TEMP_TRIGGER:   legacyForbidden,
	sqlite3.AUTH_DROP_TEMP_VIEW:      legacyForbidden,
	sqlite3.AUTH_TRANSACTION:         legacyForbidden,
	sqlite3.AUTH_SAVEPOINT:           legacyForbidden,
	sqlite3.AUTH_READ:                legacyRead,
	sqlite3.AUTH_INSERT:              legacyWrite,
	sqlite3.AUTH_UPDATE:              legacyWrite,
	sqlite3.AUTH_DELETE:              legacyWrite,
	sqlite3.AUTH_CREATE_INDEX:        legacySchema,
	sqlite3.AUTH_CREATE_TABLE:        legacySchema,
	sqlite3.AUTH_CREATE_TRIGGER:      legacySchema,
	sqlite3.AUTH_CREATE_VIEW:         legacySchema,
	sqlite3.AUTH_DROP_INDEX:          legacySchema,
	sqlite3.AUTH_DROP_TABLE:          legacySchema,
	sqlite3.AUTH_DROP_TRIGGER:        legacySchema,
	sqlite3.AUTH_DROP_VIEW:           legacySchema,
	sqlite3.AUTH_ALTER_TABLE:         legacySchema,
	sqlite3.AUTH_REINDEX:             legacySchema,
	sqlite3.AUTH_ANALYZE:             legacySchema,
	sqlite3.AUTH_FUNCTION:            legacyFunction,
}

var legacyForbiddenFunctions = map[string]bool{"load_extension": true, "readfile": true, "writefile": true}

// LegacyAuthorizer installs the SQLite authorizer of the owner's SQL tools.
func LegacyAuthorizer[T any](conn *sqlite3.Conn, mode string, state *LegacyAuthorizerState, deny func(T) sqlite3.AuthorizerReturnCode, authorizerDenial func(sqlite3.AuthorizerActionCode, string, string) T) error {
	alterMain := false
	return conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if a == sqlite3.AUTH_ALTER_TABLE && n3 == "main" && mode == "schema" {
			alterMain = true
			state.DidSchema = true
			// ALTER puts its database in n3; for DROP COLUMN, the driver
			// supplies the column name in the usual schema argument.
			return sqlite3.AUTH_OK
		}
		// ALTER checks and rewrites SQLite's temporary schema even when it has
		// no objects. Let owner schema changes reach the transactional
		// rule-set rename guard, without allowing temp objects or direct access
		// to their schema.
		if alterMain && schema == "temp" && n3 == "sqlite_temp_master" && inner == "" && (a == sqlite3.AUTH_READ || a == sqlite3.AUTH_UPDATE) {
			return sqlite3.AUTH_OK
		}
		if denied, fn, allowed := state.record(a, n3, n4, schema, mode); !allowed {
			return deny(authorizerDenial(denied, mode, fn))
		}
		return sqlite3.AUTH_OK
	})
}

// record notes what an action does to the statement and decides it for mode.
// A refused action comes back with the action to report (and the function
// name, for a refused function).
func (s *LegacyAuthorizerState) record(a sqlite3.AuthorizerActionCode, n3, n4, schema, mode string) (sqlite3.AuthorizerActionCode, string, bool) {
	switch {
	case schema == "temp":
		return sqlite3.AUTH_CREATE_TEMP_TABLE, "", false
	case schema != "" && schema != "main":
		return sqlite3.AUTH_ATTACH, "", false
	}
	switch legacyActions[a] {
	case legacyForbidden:
		return a, "", false
	case legacyRead:
		s.DidRead = true
	case legacyWrite:
		s.DidWrite = true
		if s.WriteTable == "" {
			s.WriteTable, s.WriteAction = n3, a
		}
		if mode == "execute" || mode == "schema" {
			break
		}
		if strings.HasPrefix(strings.ToLower(n3), "sqlite_") {
			// DROP writes the schema table before it reports itself.
			return sqlite3.AUTH_DROP_TABLE, "", false
		}
		return a, "", false
	case legacySchema:
		s.DidSchema = true
		if mode != "schema" {
			return a, "", false
		}
	case legacyFunction:
		if fn := strings.ToLower(n4); legacyForbiddenFunctions[fn] {
			return a, fn, false
		}
	}
	return 0, "", true
}
