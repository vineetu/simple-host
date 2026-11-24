// Package db opens Postgres, applies the embedded schema, and writes the
// events table: the one way a feature tells another that something happened.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Schema is the ops tables (events, notifications, downloads, admin counts)
// and schema_version. It references people and sites, so it is applied last.
//
//go:embed schema.sql
var Schema string

// Version is the schema version the embedded files describe.
const Version = 1

// Q is a *sql.DB or a *sql.Tx.
type Q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Open connects to Postgres at url (postgres://...) and checks it answers.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	conn, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(20)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("database does not answer: %w", err)
	}
	return conn, nil
}

// Apply creates every table on an empty database, in one transaction, from
// the modules' schema files in the order given. A database already at
// Version is left as it is.
func Apply(ctx context.Context, conn *sql.DB, schemas ...string) error {
	var done bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_version')`).Scan(&done)
	if err != nil {
		return err
	}
	if done {
		var v int
		if err := conn.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_version`).Scan(&v); err != nil {
			return err
		}
		if v != Version {
			return fmt.Errorf("database schema is version %d, this program needs %d", v, Version)
		}
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range schemas {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES ($1)`, Version); err != nil {
		return err
	}
	return tx.Commit()
}

// Event records something the owner (a person or team) may be told about.
// kind is "data" (saved data changed) or "access" (someone asked for access).
// siteID may be "". Write it in the same transaction as the change.
func Event(ctx context.Context, q Q, kind, ownerID, siteID string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var site any
	if siteID != "" {
		site = siteID
	}
	_, err = q.ExecContext(ctx, `INSERT INTO events (kind, owner_id, site_id, body) VALUES ($1, $2, $3, $4)`, kind, ownerID, site, b)
	return err
}
