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

// LegacyAuthorizer installs the legacy SQL route's SQLite authorizer.
func LegacyAuthorizer[T any](conn *sqlite3.Conn, mode, read, visitorID string, owner bool, state *LegacyAuthorizerState, deny func(T) sqlite3.AuthorizerReturnCode, authorizerDenial func(sqlite3.AuthorizerActionCode, string, string) T, visitorReadDenial func(string) T) error {
	return conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			if schema == "temp" {
				return deny(authorizerDenial(sqlite3.AUTH_CREATE_TEMP_TABLE, mode, ""))
			}
			return deny(authorizerDenial(sqlite3.AUTH_ATTACH, mode, ""))
		}
		switch a {
		case sqlite3.AUTH_ATTACH, sqlite3.AUTH_DETACH, sqlite3.AUTH_PRAGMA, sqlite3.AUTH_CREATE_VTABLE, sqlite3.AUTH_DROP_VTABLE, sqlite3.AUTH_CREATE_TEMP_TABLE, sqlite3.AUTH_CREATE_TEMP_INDEX, sqlite3.AUTH_CREATE_TEMP_TRIGGER, sqlite3.AUTH_CREATE_TEMP_VIEW, sqlite3.AUTH_DROP_TEMP_TABLE, sqlite3.AUTH_DROP_TEMP_INDEX, sqlite3.AUTH_DROP_TEMP_TRIGGER, sqlite3.AUTH_DROP_TEMP_VIEW, sqlite3.AUTH_TRANSACTION, sqlite3.AUTH_SAVEPOINT:
			return deny(authorizerDenial(a, mode, ""))
		case sqlite3.AUTH_READ:
			state.DidRead = true
			if mode == "execute" && read != "anyone" && !owner {
				if read == "owner" || visitorID == "" {
					return deny(visitorReadDenial(read))
				}
			}
		case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
			state.DidWrite = true
			if state.WriteTable == "" {
				state.WriteTable, state.WriteAction = n3, a
			}
			if mode != "execute" && mode != "schema" {
				if strings.HasPrefix(strings.ToLower(n3), "sqlite_") {
					// DROP writes the schema table before it reports itself.
					return deny(authorizerDenial(sqlite3.AUTH_DROP_TABLE, mode, ""))
				}
				return deny(authorizerDenial(a, mode, ""))
			}
		case sqlite3.AUTH_CREATE_INDEX, sqlite3.AUTH_CREATE_TABLE, sqlite3.AUTH_CREATE_TRIGGER, sqlite3.AUTH_CREATE_VIEW, sqlite3.AUTH_DROP_INDEX, sqlite3.AUTH_DROP_TABLE, sqlite3.AUTH_DROP_TRIGGER, sqlite3.AUTH_DROP_VIEW, sqlite3.AUTH_ALTER_TABLE, sqlite3.AUTH_REINDEX, sqlite3.AUTH_ANALYZE:
			state.DidSchema = true
			if mode != "schema" {
				return deny(authorizerDenial(a, mode, ""))
			}
		case sqlite3.AUTH_FUNCTION:
			fn := strings.ToLower(n4)
			if fn == "load_extension" || fn == "readfile" || fn == "writefile" {
				return deny(authorizerDenial(a, mode, fn))
			}
		}
		return sqlite3.AUTH_OK
	})
}
