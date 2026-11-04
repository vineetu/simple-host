package storage

import sqlite3 "github.com/ncruces/go-sqlite3"

// BeginRowWrite starts an immediate transaction and returns its commit and rollback operations.
func BeginRowWrite(conn *sqlite3.Conn) (func() error, func(), error) {
	if err := conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, nil, err
	}
	done := false
	commit := func() error {
		err := conn.Exec("COMMIT")
		done = err == nil
		return err
	}
	rollback := func() {
		_ = conn.SetAuthorizer(nil)
		if !done {
			_ = conn.Exec("ROLLBACK")
		}
	}
	return commit, rollback, nil
}
