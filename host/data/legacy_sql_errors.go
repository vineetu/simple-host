package data

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data/rules"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// sqlRefusal is a storage SQL error an agent can act on: what happened and
// what to do about it, under a stable code (openapi.yaml lists them). The
// connector passes the message through as the tool's error text.
//
// Visitors (legacy full-mode databases only) get SQLite's own words about
// their statement and nothing more: table and column lists, and foreign key
// targets, are for the owner.
type sqlRefusal = Error

func sqlRefuse(code, msg string) *sqlRefusal { return &Error{Code: code, Message: msg} }

const (
	sqlUseQuery   = "this statement only reads rows: send it to storage_sql_query (REST: POST .../query)"
	sqlUseExecute = "this statement changes rows: send it to storage_sql_execute (REST: POST .../execute); storage_sql_query only reads"
	sqlUseSchema  = "this statement changes the schema (CREATE, ALTER, DROP): send it to storage_sql_schema (REST: POST .../schema), as the site's owner"
)

// sqlAuthorizerDenial says why the authorizer refused an action, so a
// refused Prepare can name the rule instead of "not authorized".
func sqlAuthorizerDenial(a sqlite3.AuthorizerActionCode, mode, fn string) *sqlRefusal {
	switch a {
	case sqlite3.AUTH_ATTACH, sqlite3.AUTH_DETACH:
		return sqlRefuse("sql_not_available", "ATTACH and DETACH are not available here: each resource is one database, so query another resource with its own call")
	case sqlite3.AUTH_PRAGMA:
		return sqlRefuse("sql_not_available", "PRAGMA is not available here (pragma_* table functions neither). To see the tables and their columns: SELECT name, sql FROM sqlite_schema")
	case sqlite3.AUTH_CREATE_VTABLE, sqlite3.AUTH_DROP_VTABLE:
		return sqlRefuse("sql_not_available", "virtual tables (CREATE VIRTUAL TABLE, FTS) are not available here: use a normal table")
	case sqlite3.AUTH_CREATE_TEMP_TABLE, sqlite3.AUTH_CREATE_TEMP_INDEX, sqlite3.AUTH_CREATE_TEMP_TRIGGER, sqlite3.AUTH_CREATE_TEMP_VIEW,
		sqlite3.AUTH_DROP_TEMP_TABLE, sqlite3.AUTH_DROP_TEMP_INDEX, sqlite3.AUTH_DROP_TEMP_TRIGGER, sqlite3.AUTH_DROP_TEMP_VIEW:
		return sqlRefuse("sql_not_available", "temp tables, views, indexes and triggers are not available here: use a WITH clause (CTE) or a subquery, or a normal table")
	case sqlite3.AUTH_TRANSACTION, sqlite3.AUTH_SAVEPOINT:
		return sqlRefuse("sql_not_available", "BEGIN, COMMIT, ROLLBACK and SAVEPOINT are not available: every call is its own transaction, so send one statement per call")
	case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
		return sqlRefuse("sql_use_execute", sqlUseExecute)
	case sqlite3.AUTH_FUNCTION:
		return sqlRefuse("sql_not_available", fn+"() is not available here")
	}
	if mode != "schema" {
		return sqlRefuse("sql_use_schema", sqlUseSchema)
	}
	return sqlRefuse("sql_not_available", "that is not available here")
}

// sqlRouteMismatch explains a statement that compiled but belongs to another
// route. It returns nil when the statement fits mode.
func sqlRouteMismatch(mode, sql string, readOnly, didWrite, didSchema, noopCreate bool) *sqlRefusal {
	switch mode {
	case "query":
		if didSchema {
			return sqlRefuse("sql_use_schema", sqlUseSchema)
		}
		if !readOnly || didWrite {
			return sqlRefuse("sql_use_execute", sqlUseExecute)
		}
	case "execute":
		if didSchema || !didWrite && sqlLooksLikeSchema(sql) {
			return sqlRefuse("sql_use_schema", sqlUseSchema)
		}
		if !didWrite {
			return sqlRefuse("sql_use_query", sqlUseQuery+"; storage_sql_execute is for INSERT, UPDATE and DELETE")
		}
	case "schema":
		if didSchema || noopCreate {
			return nil
		}
		if didWrite {
			return sqlRefuse("sql_use_execute", "this statement changes rows, not the schema: send it to storage_sql_execute (REST: POST .../execute)")
		}
		return sqlRefuse("sql_use_query", sqlUseQuery+"; storage_sql_schema is for CREATE, ALTER and DROP")
	}
	return nil
}

func sqlLooksLikeSchema(sql string) bool { return rules.SQLLooksLikeSchema(sql) }

func sqlSecondStatement(tail string) string { return rules.SQLSecondStatement(tail) }

func sqlMultipleStatements(second string) *sqlRefusal {
	return sqlRefuse("sql_multiple_statements", rules.SQLMultipleStatements(second))
}

func sqlParamCount(placeholders, params int) *sqlRefusal {
	return sqlRefuse("sql_param_count", rules.SQLParamCount(placeholders, params))
}

func sqlClip(s string, n int) string { return rules.SQLClip(s, n) }

func sqliteText(err error) string {
	var se *sqlite3.Error
	if errors.As(err, &se) {
		return rules.SQLiteText(err.Error(), se.ExtendedCode().Error()+": ", se.Code().Error()+": ")
	}
	return rules.SQLiteText(err.Error())
}

var (
	sqlNoTableRe  = rules.SQLNoTableRE
	sqlNoColumnRe = rules.SQLNoColumnRE
	sqlNoNamedRe  = rules.SQLNoNamedRE
	sqlNearRe     = rules.SQLNearRE
	sqlUniqueRe   = rules.SQLUniqueRE
	sqlNotNullRe  = rules.SQLNotNullRE
	sqlCheckRe    = rules.SQLCheckRE
)

// sqlFailure turns a SQLite error (compiling or running the statement) into
// a refusal. owner decides whether the real table and column names may be
// listed; conn must be the connection that failed, with the request's
// authorizer still set (it is cleared here before the server's own lookups).
func sqlFailure(ctx context.Context, conn *sqlite3.Conn, err error, owner bool, sql, writeTable string, writeAction sqlite3.AuthorizerActionCode, timeout time.Duration) *sqlRefusal {
	switch {
	case errors.Is(err, sqlite3.INTERRUPT) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return sqlRefuse("sql_timeout", fmt.Sprintf("the statement ran longer than %s and was stopped: add a LIMIT, a narrower WHERE, or an index", timeout))
	case errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED):
		return sqlRefuse("storage_busy", "the database is busy with another change; call once more in a second")
	case errors.Is(err, sqlite3.TOOBIG):
		return sqlRefuse("sql_value_too_large", "a value or the statement is too large: each value may be at most 1,048,576 bytes and a statement 32,768 characters")
	}
	msg := sqliteText(err)
	if conn != nil {
		_ = conn.SetAuthorizer(nil)
	}
	if r := sqlMissingName(conn, owner, sql, msg); r != nil {
		return r
	}
	if r := sqlSyntax(msg); r != nil {
		return r
	}
	if r := sqlConstraint(conn, err, owner, msg, writeTable, writeAction); r != nil {
		return r
	}
	switch {
	case strings.HasPrefix(msg, "table ") && strings.Contains(msg, " values were supplied"):
		if !owner {
			msg = "the number of values does not match the table's columns"
		}
		return sqlRefuse("sql_error", msg+": name the columns, e.g. INSERT INTO t (a, b) VALUES (?, ?)")
	case strings.HasPrefix(msg, "no such function: "):
		fn := strings.TrimPrefix(msg, "no such function: ")
		return sqlRefuse("sql_no_such_function", fn+"() is not available here: check the name against SQLite's built-in functions (dates use datetime('now'), joining text uses ||)")
	case errors.Is(err, sqlite3.AUTH) && strings.Contains(strings.ToLower(sql), "pragma"):
		return sqlAuthorizerDenial(sqlite3.AUTH_PRAGMA, "", "")
	case errors.Is(err, sqlite3.AUTH):
		return sqlRefuse("sql_not_available", "that statement is not available here")
	}
	return sqlRefuse("sql_error", "SQLite: "+msg)
}

// sqlMissingName explains a table or column that does not exist, listing the
// real names to the owner.
func sqlMissingName(conn *sqlite3.Conn, owner bool, sql, msg string) *sqlRefusal {
	if m := sqlNoTableRe.FindStringSubmatch(msg); m != nil {
		out := "table " + m[1] + " does not exist"
		if owner {
			if tables := sqlTableNames(conn); len(tables) == 0 {
				out += ". This database has no tables yet: create one with storage_sql_schema"
			} else {
				out += ". Tables here: " + strings.Join(tables, ", ")
			}
		}
		return sqlRefuse("sql_no_such_table", out)
	}
	if m := sqlNoNamedRe.FindStringSubmatch(msg); m != nil {
		out := "column " + m[2] + " does not exist in " + m[1]
		if cols := sqlColumnNames(conn, m[1]); owner && len(cols) > 0 {
			out += ". Columns in " + m[1] + ": " + strings.Join(cols, ", ")
		}
		return sqlRefuse("sql_no_such_column", out)
	}
	if m := sqlNoColumnRe.FindStringSubmatch(msg); m != nil {
		out := "column " + m[1] + " does not exist"
		if owner {
			for _, t := range sqlTablesIn(conn, sql) {
				if cols := sqlColumnNames(conn, t); len(cols) > 0 {
					out += ". Columns in " + t + ": " + strings.Join(cols, ", ")
				}
			}
		}
		return sqlRefuse("sql_no_such_column", out+". Text values go in single quotes or in params, never in double quotes")
	}
	return nil
}

// sqlSyntax explains a statement SQLite could not parse.
func sqlSyntax(msg string) *sqlRefusal {
	if m := sqlNearRe.FindStringSubmatch(msg); m != nil {
		return sqlRefuse("sql_syntax", fmt.Sprintf("SQLite syntax error near %q: fix the statement at that word (put values in params with ? placeholders)", sqlClip(m[1], 60)))
	}
	if msg == "incomplete input" {
		return sqlRefuse("sql_syntax", "SQLite syntax error: the statement ends too early (incomplete input); look for a missing closing bracket or quote, or a clause cut short")
	}
	if strings.HasPrefix(msg, "unrecognized token:") {
		return sqlRefuse("sql_syntax", "SQLite syntax error: "+msg+"; check the quotes (text in single quotes) and brackets")
	}
	return nil
}

// sqlConstraintRefusals explain the constraint messages that name what
// failed. Constraint messages can name columns the statement never
// mentioned, so only the owner sees the name (owner, filled with it).
var sqlConstraintRefusals = []struct {
	re             *regexp.Regexp
	code           string
	visitor, owner string
	clip           int
}{
	{sqlUniqueRe, "sql_unique_failed", "a row with the same unique value already exists (UNIQUE): use another value",
		"a row with the same %s already exists (UNIQUE): use another value, or change the existing row with UPDATE (or INSERT ... ON CONFLICT DO UPDATE)", 0},
	{sqlNotNullRe, "sql_not_null_failed", "a required column got no value (NOT NULL): fill in every required column",
		"%s needs a value (NOT NULL): include it in the statement and do not send null for it", 0},
	{sqlCheckRe, "sql_check_failed", "a value breaks one of the table's CHECK rules: send a value that meets it",
		"a value breaks the table's CHECK rule (%s): send a value that meets it", 120},
}

// sqlConstraint explains a table rule (UNIQUE, NOT NULL, CHECK, FOREIGN KEY
// or another constraint) that refused the change.
func sqlConstraint(conn *sqlite3.Conn, err error, owner bool, msg, writeTable string, writeAction sqlite3.AuthorizerActionCode) *sqlRefusal {
	for _, c := range sqlConstraintRefusals {
		m := c.re.FindStringSubmatch(msg)
		switch {
		case m == nil:
		case !owner:
			return sqlRefuse(c.code, c.visitor)
		case c.clip > 0:
			return sqlRefuse(c.code, fmt.Sprintf(c.owner, sqlClip(m[1], c.clip)))
		default:
			return sqlRefuse(c.code, fmt.Sprintf(c.owner, m[1]))
		}
	}
	switch {
	case msg == "FOREIGN KEY constraint failed":
		return sqlForeignKey(conn, owner, writeTable, writeAction)
	case !errors.Is(err, sqlite3.CONSTRAINT):
		return nil
	case !owner:
		return sqlRefuse("sql_constraint_failed", "a table rule refused the change")
	}
	return sqlRefuse("sql_constraint_failed", "a table rule refused the change: "+msg)
}

func sqlForeignKey(conn *sqlite3.Conn, owner bool, table string, action sqlite3.AuthorizerActionCode) *sqlRefusal {
	if action == sqlite3.AUTH_DELETE {
		return sqlRefuse("sql_foreign_key_failed", "other rows still point at this row (FOREIGN KEY): delete or change those rows first")
	}
	out := "a value must match an existing row in the table it points at (FOREIGN KEY)"
	if owner && table != "" && conn != nil {
		if refs := sqlForeignKeys(conn, table); len(refs) > 0 {
			out = "a value must match an existing row (FOREIGN KEY): " + strings.Join(refs, "; ")
		}
	}
	return sqlRefuse("sql_foreign_key_failed", out+". Add that row first, or use a value that exists")
}

// sqlTableNames lists the database's own tables, sorted, for the owner.
func sqlTableNames(conn *sqlite3.Conn) []string {
	if conn == nil {
		return nil
	}
	set, err := runtime.SQLTables(conn)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(set))
	for t := range set {
		names = append(names, t)
	}
	sort.Strings(names)
	if len(names) > 30 {
		names = append(names[:30], fmt.Sprintf("and %d more", len(names)-30))
	}
	return names
}

func sqlColumnNames(conn *sqlite3.Conn, table string) []string {
	return runtime.LegacyColumnNames(conn, table)
}

// sqlTablesIn names the database's tables that the statement mentions, at
// most three, so a missing column can be shown next to the real ones.
func sqlTablesIn(conn *sqlite3.Conn, sql string) []string {
	return rules.SQLTablesIn(sqlTableNames(conn), sql)
}

func sqlForeignKeys(conn *sqlite3.Conn, table string) []string {
	return runtime.LegacyForeignKeys(conn, table)
}
