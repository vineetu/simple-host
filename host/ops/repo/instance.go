package repo

import (
	"context"
	"database/sql"
	"errors"
)

// ErrUnconfirmed: the commit failed, so the hostnames may or may not be saved.
var ErrUnconfirmed = errors.New("instance hosts: commit not confirmed")

// InstanceHosts reads the hostnames the setup page saved; empty strings mean
// setup is unfinished.
func InstanceHosts(ctx context.Context, db *sql.DB) (siteDomain, contentHost string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM instance_config WHERE key IN ('site_domain', 'content_host')`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return "", "", err
		}
		if key == "site_domain" {
			siteDomain = value
		} else {
			contentHost = value
		}
	}
	return siteDomain, contentHost, rows.Err()
}

// SaveInstanceHosts saves the two hostnames unless setup already finished,
// in one transaction that holds the site_domain row: saved is false when
// another request finished setup first.
func SaveInstanceHosts(ctx context.Context, db *sql.DB, siteDomain, contentHost string) (saved bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Establish a lockable row even on a fresh database. A concurrent insert
	// waits for this transaction, then FOR UPDATE sees its committed value.
	if _, err = tx.ExecContext(ctx, `INSERT INTO instance_config (key, value) VALUES ('site_domain', '') ON CONFLICT (key) DO NOTHING`); err != nil {
		return false, err
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM instance_config WHERE key='site_domain' FOR UPDATE`).Scan(&current); err != nil {
		return false, err
	}
	if current != "" {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO instance_config (key, value) VALUES ('site_domain', $1), ('content_host', $2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, siteDomain, contentHost); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, errors.Join(ErrUnconfirmed, err)
	}
	return true, nil
}
