package storage

import (
	"errors"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// Error identifies a storage workflow failure.
type Error struct {
	Code, Message string
	Rule          string
	Allowed       bool
	NoRetryAfter  bool
	Refusals      []*Error
}

func (e *Error) Error() string { return e.Message }

func sqliteFailure(err error) error {
	if errors.Is(err, sqlite3.NOMEM) {
		return &Error{Code: "query_too_expensive", Message: "query exceeds SQLite memory limit"}
	}
	return err
}
func sqlitePanicError(p any) error {
	if err, ok := p.(error); ok {
		if errors.Is(err, sqlite3.NOMEM) || err.Error() == "sqlite3: out of memory" {
			return sqliteFailure(sqlite3.NOMEM)
		}
	}
	if message, ok := p.(string); ok && message == "sqlite3: out of memory" {
		return sqliteFailure(sqlite3.NOMEM)
	}
	return &Error{Code: "storage_failed", Message: "storage operation failed"}
}

// sqliteRowFailure retains the row route's normal diagnostic except for NOMEM.
func sqliteRowFailure(err error, fallback *Error) error {
	if errors.Is(err, sqlite3.NOMEM) {
		return sqliteFailure(err)
	}
	return fallback
}
