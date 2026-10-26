package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// sqlRefusal is a storage SQL error an agent can act on: what happened and
// what to do about it, under a stable code (openapi.yaml lists them). The
// connector passes the message through as the tool's error text.
//
// Visitors (legacy full-mode databases only) get SQLite's own words about
// their statement and nothing more: table and column lists, and foreign key
// targets, are for the owner.
type sqlRefusal struct {
	status    int
	code, msg string
}

func (r *sqlRefusal) write(w http.ResponseWriter) { storageError(w, r.status, r.code, r.msg) }

func sqlRefuse(status int, code, msg string) *sqlRefusal {
	return &sqlRefusal{status: status, code: code, msg: msg}
}

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
		return sqlRefuse(400, "sql_not_available", "ATTACH and DETACH are not available here: each resource is one database, so query another resource with its own call")
	case sqlite3.AUTH_PRAGMA:
		return sqlRefuse(400, "sql_not_available", "PRAGMA is not available here (pragma_* table functions neither). To see the tables and their columns: SELECT name, sql FROM sqlite_schema")
	case sqlite3.AUTH_CREATE_VTABLE, sqlite3.AUTH_DROP_VTABLE:
		return sqlRefuse(400, "sql_not_available", "virtual tables (CREATE VIRTUAL TABLE, FTS) are not available here: use a normal table")
	case sqlite3.AUTH_CREATE_TEMP_TABLE, sqlite3.AUTH_CREATE_TEMP_INDEX, sqlite3.AUTH_CREATE_TEMP_TRIGGER, sqlite3.AUTH_CREATE_TEMP_VIEW,
		sqlite3.AUTH_DROP_TEMP_TABLE, sqlite3.AUTH_DROP_TEMP_INDEX, sqlite3.AUTH_DROP_TEMP_TRIGGER, sqlite3.AUTH_DROP_TEMP_VIEW:
		return sqlRefuse(400, "sql_not_available", "temp tables, views, indexes and triggers are not available here: use a WITH clause (CTE) or a subquery, or a normal table")
	case sqlite3.AUTH_TRANSACTION, sqlite3.AUTH_SAVEPOINT:
		return sqlRefuse(400, "sql_not_available", "BEGIN, COMMIT, ROLLBACK and SAVEPOINT are not available: every call is its own transaction, so send one statement per call")
	case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
		return sqlRefuse(400, "sql_use_execute", sqlUseExecute)
	case sqlite3.AUTH_FUNCTION:
		return sqlRefuse(400, "sql_not_available", fn+"() is not available here")
	}
	if mode != "schema" {
		return sqlRefuse(400, "sql_use_schema", sqlUseSchema)
	}
	return sqlRefuse(400, "sql_not_available", "that is not available here")
}

// sqlVisitorReadDenial explains a visitor statement that reads rows it may
// not read. It names the database's rule, never the rows.
func sqlVisitorReadDenial(read string) *sqlRefusal {
	if read == "owner" {
		return sqlRefuse(403, "sql_visitor_rule", "this database's read rule is owner, so a visitor's statement cannot read its rows (no WHERE, RETURNING or subquery on its tables): write without reading")
	}
	return sqlRefuse(403, "sql_visitor_rule", "this database's read rule is "+read+": a visitor's statement that reads rows needs the visitor signed in on the site's own address first")
}

// sqlRouteMismatch explains a statement that compiled but belongs to another
// route. It returns nil when the statement fits mode.
func sqlRouteMismatch(mode, sql string, readOnly, didWrite, didSchema, noopCreate bool) *sqlRefusal {
	switch mode {
	case "query":
		if didSchema {
			return sqlRefuse(400, "sql_use_schema", sqlUseSchema)
		}
		if !readOnly || didWrite {
			return sqlRefuse(400, "sql_use_execute", sqlUseExecute)
		}
	case "execute":
		if didSchema || !didWrite && sqlLooksLikeSchema(sql) {
			return sqlRefuse(400, "sql_use_schema", sqlUseSchema)
		}
		if !didWrite {
			return sqlRefuse(400, "sql_use_query", sqlUseQuery+"; storage_sql_execute is for INSERT, UPDATE and DELETE")
		}
	case "schema":
		if didSchema || noopCreate {
			return nil
		}
		if didWrite {
			return sqlRefuse(400, "sql_use_execute", "this statement changes rows, not the schema: send it to storage_sql_execute (REST: POST .../execute)")
		}
		return sqlRefuse(400, "sql_use_query", sqlUseQuery+"; storage_sql_schema is for CREATE, ALTER and DROP")
	}
	return nil
}

func sqlLooksLikeSchema(sql string) bool {
	word := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range []string{"CREATE", "ALTER", "DROP"} {
		if strings.HasPrefix(word, kw) {
			return true
		}
	}
	return false
}

// sqlSecondStatement reports the start of a second statement in what Prepare
// left over, ignoring blank space, stray semicolons and comments.
func sqlSecondStatement(tail string) string {
	rest := tail
	for {
		rest = strings.TrimLeft(rest, " \t\r\n;")
		switch {
		case strings.HasPrefix(rest, "--"):
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				rest = rest[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(rest, "/*"):
			if i := strings.Index(rest[2:], "*/"); i >= 0 {
				rest = rest[i+4:]
				continue
			}
			return ""
		}
		return rest
	}
}

func sqlMultipleStatements(second string) *sqlRefusal {
	return sqlRefuse(400, "sql_multiple_statements", fmt.Sprintf("this call holds more than one statement (the second starts %q): send one statement per call", sqlClip(second, 40)))
}

func sqlParamCount(placeholders, params int) *sqlRefusal {
	return sqlRefuse(400, "sql_param_count", fmt.Sprintf("the statement has %d placeholder%s but %d param%s were sent: send exactly one value in params for each ? (in order)", placeholders, plural(placeholders), params, plural(params)))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sqlClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// sqliteText is SQLite's own message without the driver's prefix, with
// anything that looks like a file path removed.
func sqliteText(err error) string {
	msg := err.Error()
	var se *sqlite3.Error
	if errors.As(err, &se) {
		for _, prefix := range []string{se.ExtendedCode().Error() + ": ", se.Code().Error() + ": "} {
			msg = strings.TrimPrefix(msg, prefix)
		}
	}
	msg = strings.TrimPrefix(msg, "sqlite3: ")
	msg = sqlPathPattern.ReplaceAllString(msg, "[path]")
	return sqlClip(msg, 300)
}

// sqlPathPattern matches absolute paths and database file names; SQLite's
// compile messages never need them, and the server's layout stays private.
var sqlPathPattern = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+){2,}/?|[A-Za-z0-9._-]+\.sqlite(?:-wal|-shm)?\b`)

var (
	sqlNoTableRe  = regexp.MustCompile(`^no such table: (?:main\.)?(.+)$`)
	sqlNoColumnRe = regexp.MustCompile(`^no such column: (.+)$`)
	sqlNoNamedRe  = regexp.MustCompile(`^table (.+) has no column named (.+)$`)
	sqlNearRe     = regexp.MustCompile(`^near "(.*)": syntax error$`)
	sqlUniqueRe   = regexp.MustCompile(`^UNIQUE constraint failed: (.+)$`)
	sqlNotNullRe  = regexp.MustCompile(`^NOT NULL constraint failed: (.+)$`)
	sqlCheckRe    = regexp.MustCompile(`^CHECK constraint failed: (.+)$`)
)

// sqlFailure turns a SQLite error (compiling or running the statement) into
// a refusal. owner decides whether the real table and column names may be
// listed; conn must be the connection that failed, with the request's
// authorizer still set (it is cleared here before the server's own lookups).
func sqlFailure(ctx context.Context, conn *sqlite3.Conn, err error, owner bool, sql, writeTable string, writeAction sqlite3.AuthorizerActionCode, timeout time.Duration) *sqlRefusal {
	if errors.Is(err, sqlite3.INTERRUPT) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return sqlRefuse(400, "sql_timeout", fmt.Sprintf("the statement ran longer than %s and was stopped: add a LIMIT, a narrower WHERE, or an index", timeout))
	}
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return sqlRefuse(503, "storage_busy", "the database is busy with another change; call once more in a second")
	}
	if errors.Is(err, sqlite3.TOOBIG) {
		return sqlRefuse(400, "sql_value_too_large", "a value or the statement is too large: each value may be at most 1,048,576 bytes and a statement 32,768 characters")
	}
	msg := sqliteText(err)
	if conn != nil {
		_ = conn.SetAuthorizer(nil)
	}
	if m := sqlNoTableRe.FindStringSubmatch(msg); m != nil {
		out := "table " + m[1] + " does not exist"
		if owner {
			if tables := sqlTableNames(conn); len(tables) == 0 {
				out += ". This database has no tables yet: create one with storage_sql_schema"
			} else {
				out += ". Tables here: " + strings.Join(tables, ", ")
			}
		}
		return sqlRefuse(400, "sql_no_such_table", out)
	}
	if m := sqlNoNamedRe.FindStringSubmatch(msg); m != nil {
		out := "column " + m[2] + " does not exist in " + m[1]
		if cols := sqlColumnNames(conn, m[1]); owner && len(cols) > 0 {
			out += ". Columns in " + m[1] + ": " + strings.Join(cols, ", ")
		}
		return sqlRefuse(400, "sql_no_such_column", out)
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
		return sqlRefuse(400, "sql_no_such_column", out+". Text values go in single quotes or in params, never in double quotes")
	}
	if m := sqlNearRe.FindStringSubmatch(msg); m != nil {
		return sqlRefuse(400, "sql_syntax", fmt.Sprintf("SQLite syntax error near %q: fix the statement at that word (put values in params with ? placeholders)", sqlClip(m[1], 60)))
	}
	if msg == "incomplete input" {
		return sqlRefuse(400, "sql_syntax", "SQLite syntax error: the statement ends too early (incomplete input); look for a missing closing bracket or quote, or a clause cut short")
	}
	if strings.HasPrefix(msg, "unrecognized token:") {
		return sqlRefuse(400, "sql_syntax", "SQLite syntax error: "+msg+"; check the quotes (text in single quotes) and brackets")
	}
	// Constraint messages can name columns the statement never mentioned,
	// so only the owner sees the names.
	if m := sqlUniqueRe.FindStringSubmatch(msg); m != nil {
		if !owner {
			return sqlRefuse(409, "sql_unique_failed", "a row with the same unique value already exists (UNIQUE): use another value")
		}
		return sqlRefuse(409, "sql_unique_failed", "a row with the same "+m[1]+" already exists (UNIQUE): use another value, or change the existing row with UPDATE (or INSERT ... ON CONFLICT DO UPDATE)")
	}
	if m := sqlNotNullRe.FindStringSubmatch(msg); m != nil {
		if !owner {
			return sqlRefuse(400, "sql_not_null_failed", "a required column got no value (NOT NULL): fill in every required column")
		}
		return sqlRefuse(400, "sql_not_null_failed", m[1]+" needs a value (NOT NULL): include it in the statement and do not send null for it")
	}
	if m := sqlCheckRe.FindStringSubmatch(msg); m != nil {
		if !owner {
			return sqlRefuse(400, "sql_check_failed", "a value breaks one of the table's CHECK rules: send a value that meets it")
		}
		return sqlRefuse(400, "sql_check_failed", "a value breaks the table's CHECK rule ("+sqlClip(m[1], 120)+"): send a value that meets it")
	}
	if msg == "FOREIGN KEY constraint failed" {
		return sqlForeignKey(conn, owner, writeTable, writeAction)
	}
	if errors.Is(err, sqlite3.CONSTRAINT) {
		if !owner {
			return sqlRefuse(400, "sql_constraint_failed", "a table rule refused the change")
		}
		return sqlRefuse(400, "sql_constraint_failed", "a table rule refused the change: "+msg)
	}
	if strings.HasPrefix(msg, "table ") && strings.Contains(msg, " values were supplied") {
		if !owner {
			msg = "the number of values does not match the table's columns"
		}
		return sqlRefuse(400, "sql_error", msg+": name the columns, e.g. INSERT INTO t (a, b) VALUES (?, ?)")
	}
	if strings.HasPrefix(msg, "no such function: ") {
		fn := strings.TrimPrefix(msg, "no such function: ")
		return sqlRefuse(400, "sql_no_such_function", fn+"() is not available here: check the name against SQLite's built-in functions (dates use datetime('now'), joining text uses ||)")
	}
	if errors.Is(err, sqlite3.AUTH) {
		if strings.Contains(strings.ToLower(sql), "pragma") {
			return sqlAuthorizerDenial(sqlite3.AUTH_PRAGMA, "", "")
		}
		return sqlRefuse(400, "sql_not_available", "that statement is not available here")
	}
	return sqlRefuse(400, "sql_error", "SQLite: "+msg)
}

func sqlForeignKey(conn *sqlite3.Conn, owner bool, table string, action sqlite3.AuthorizerActionCode) *sqlRefusal {
	if action == sqlite3.AUTH_DELETE {
		return sqlRefuse(400, "sql_foreign_key_failed", "other rows still point at this row (FOREIGN KEY): delete or change those rows first")
	}
	out := "a value must match an existing row in the table it points at (FOREIGN KEY)"
	if owner && table != "" && conn != nil {
		if refs := sqlForeignKeys(conn, table); len(refs) > 0 {
			out = "a value must match an existing row (FOREIGN KEY): " + strings.Join(refs, "; ")
		}
	}
	return sqlRefuse(400, "sql_foreign_key_failed", out+". Add that row first, or use a value that exists")
}

// sqlTableNames lists the database's own tables, sorted, for the owner.
func sqlTableNames(conn *sqlite3.Conn) []string {
	if conn == nil {
		return nil
	}
	set, err := storageSQLTables(conn)
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
	if conn == nil {
		return nil
	}
	stmt, _, err := conn.Prepare("SELECT name FROM pragma_table_info(?) ORDER BY cid")
	if err != nil {
		return nil
	}
	defer stmt.Close()
	if stmt.BindText(1, table) != nil {
		return nil
	}
	var cols []string
	for stmt.Step() && len(cols) < 60 {
		cols = append(cols, stmt.ColumnText(0))
	}
	return cols
}

// sqlTablesIn names the database's tables that the statement mentions, at
// most three, so a missing column can be shown next to the real ones.
func sqlTablesIn(conn *sqlite3.Conn, sql string) []string {
	words := map[string]bool{}
	for _, w := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(sql, -1) {
		words[strings.ToLower(w)] = true
	}
	var out []string
	for _, t := range sqlTableNames(conn) {
		if words[strings.ToLower(t)] && len(out) < 3 {
			out = append(out, t)
		}
	}
	return out
}

func sqlForeignKeys(conn *sqlite3.Conn, table string) []string {
	stmt, _, err := conn.Prepare(`SELECT "from", "table", coalesce("to", '') FROM pragma_foreign_key_list(?)`)
	if err != nil {
		return nil
	}
	defer stmt.Close()
	if stmt.BindText(1, table) != nil {
		return nil
	}
	var refs []string
	for stmt.Step() && len(refs) < 5 {
		to := stmt.ColumnText(2)
		if to == "" {
			to = "its primary key"
		}
		refs = append(refs, table+"."+stmt.ColumnText(0)+" must match "+stmt.ColumnText(1)+"."+to)
	}
	return refs
}
