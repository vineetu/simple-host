package storage

import (
	"fmt"
	"regexp"
	"strings"
)

// SQLPathPattern matches absolute paths and database file names; SQLite's
// compile messages never need them, and the server's layout stays private.
var SQLPathPattern = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+){2,}/?|[A-Za-z0-9._-]+\.sqlite(?:-wal|-shm)?\b`)

// SQLLooksLikeSchema reports whether a statement starts with a schema keyword.
func SQLLooksLikeSchema(sql string) bool {
	word := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range []string{"CREATE", "ALTER", "DROP"} {
		if strings.HasPrefix(word, kw) {
			return true
		}
	}
	return false
}

// SQLSecondStatement reports the start of a second statement in what Prepare
// left over, ignoring blank space, stray semicolons and comments.
func SQLSecondStatement(tail string) string {
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

// Plural returns the suffix for a count.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// SQLClip collapses whitespace and limits text to n runes.
func SQLClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// SQLiteText removes driver prefixes and file paths from a SQLite message.
// The caller supplies the extended and base SQLite code prefixes in that order;
// the generic sqlite3 prefix is removed after them.
func SQLiteText(msg string, prefixes ...string) string {
	for _, prefix := range prefixes {
		msg = strings.TrimPrefix(msg, prefix)
	}
	msg = strings.TrimPrefix(msg, "sqlite3: ")
	msg = SQLPathPattern.ReplaceAllString(msg, "[path]")
	return SQLClip(msg, 300)
}

// SQLMultipleStatements describes a second SQL statement.
func SQLMultipleStatements(second string) string {
	return fmt.Sprintf("this call holds more than one statement (the second starts %q): run each statement as its own call, one after another", SQLClip(second, 40))
}

// SQLParamCount describes a parameter count mismatch.
func SQLParamCount(placeholders, params int) string {
	return fmt.Sprintf("the statement has %d placeholder%s but %d param%s were sent: send exactly one value in params for each ? (in order)", placeholders, Plural(placeholders), params, Plural(params))
}

var (
	SQLNoTableRE  = regexp.MustCompile(`^no such table: (?:main\.)?(.+)$`)
	SQLNoColumnRE = regexp.MustCompile(`^no such column: (.+)$`)
	SQLNoNamedRE  = regexp.MustCompile(`^table (.+) has no column named (.+)$`)
	SQLNearRE     = regexp.MustCompile(`^near "(.*)": syntax error$`)
	SQLUniqueRE   = regexp.MustCompile(`^UNIQUE constraint failed: (.+)$`)
	SQLNotNullRE  = regexp.MustCompile(`^NOT NULL constraint failed: (.+)$`)
	SQLCheckRE    = regexp.MustCompile(`^CHECK constraint failed: (.+)$`)
)

// SQLTablesIn returns up to three listed table names mentioned in a statement.
func SQLTablesIn(tables []string, sql string) []string {
	words := map[string]bool{}
	for _, w := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(sql, -1) {
		words[strings.ToLower(w)] = true
	}
	var out []string
	for _, t := range tables {
		if words[strings.ToLower(t)] && len(out) < 3 {
			out = append(out, t)
		}
	}
	return out
}
