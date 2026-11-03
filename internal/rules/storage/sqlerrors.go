package storage

import (
	"errors"
	"fmt"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"regexp"
	"strings"
)

// SQLPathPattern provides the storage s q l path pattern.
var SQLPathPattern = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+){2,}/?|[A-Za-z0-9._-]+\.sqlite(?:-wal|-shm)?\b`)

// SQLLooksLikeSchema provides the storage s q l looks like schema.
func SQLLooksLikeSchema(sql string) bool {
	word := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range []string{"CREATE", "ALTER", "DROP"} {
		if strings.HasPrefix(word, kw) {
			return true
		}
	}
	return false
}

// SQLSecondStatement provides the storage s q l second statement.
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

// Plural provides the storage plural.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// SQLClip provides the storage s q l clip.
func SQLClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// SQLiteText provides the storage s q lite text.
func SQLiteText(err error) string {
	msg := err.Error()
	var se *sqlite3.Error
	if errors.As(err, &se) {
		for _, prefix := range []string{se.ExtendedCode().Error() + ": ", se.Code().Error() + ": "} {
			msg = strings.TrimPrefix(msg, prefix)
		}
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
