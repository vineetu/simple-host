package migrations

import (
	"context"
	"database/sql"
)

// Lock-taking forms of ApplyConn, MarkConn and InitConn for the tests.

func (s Set) Apply(ctx context.Context, db *sql.DB, logf func(string, ...any)) ([]string, error) {
	var ran []string
	err := WithLock(ctx, db, func(conn *sql.Conn) error {
		var err error
		ran, err = s.ApplyConn(ctx, conn, logf)
		return err
	})
	return ran, err
}

func (s Set) Mark(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var added bool
	err := WithLock(ctx, db, func(conn *sql.Conn) error {
		var err error
		added, err = s.MarkConn(ctx, conn, name)
		return err
	})
	return added, err
}

func (s Set) Init(ctx context.Context, db *sql.DB, logf func(string, ...any)) (bool, error) {
	built := false
	err := WithLock(ctx, db, func(conn *sql.Conn) error {
		var err error
		built, err = s.InitConn(ctx, conn, "", logf)
		return err
	})
	return built, err
}
