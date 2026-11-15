package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// KVNamespace includes the facts needed to resolve live and trashed directories.
type KVNamespace struct {
	SiteID, Name, Store, OwnerID, SiteName string
	Deleted                                bool
}

func KVMoveNamespaces(ctx context.Context, db *sql.DB) ([]KVNamespace, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.site_id,r.name,r.kv_store,s.user_id,s.name,s.deleted_at IS NOT NULL FROM site_storage_resources r JOIN sites s ON s.id=r.site_id WHERE r.kind='kv' ORDER BY r.site_id,r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KVNamespace
	for rows.Next() {
		var n KVNamespace
		if err := rows.Scan(&n.SiteID, &n.Name, &n.Store, &n.OwnerID, &n.SiteName, &n.Deleted); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// KVMoveEntry preserves Postgres's exact value text and microsecond timestamps.
type KVMoveEntry struct {
	Key, Value, Writer string
	Created, Updated   time.Time
}

// KVMoveSnapshot reads rows, order and logical usage from one MVCC snapshot.
func KVMoveSnapshot(ctx context.Context, db *sql.DB, n KVNamespace) ([]KVMoveEntry, int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT key,value::text,writer_id,created_at,updated_at FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 ORDER BY key`, n.SiteID, n.Name)
	if err != nil {
		return nil, 0, err
	}
	var out []KVMoveEntry
	for rows.Next() {
		var e KVMoveEntry
		if err = rows.Scan(&e.Key, &e.Value, &e.Writer, &e.Created, &e.Updated); err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	var used int64
	err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(octet_length(key)+octet_length(value::text)),0) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2`, n.SiteID, n.Name).Scan(&used)
	if err != nil {
		return nil, 0, err
	}
	return out, used, tx.Commit()
}

// KVMoveCommit flips the marker and removes the proven source copy atomically.
func KVMoveCommit(ctx context.Context, db *sql.DB, n KVNamespace, count int64, beforeCommit func() error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE site_storage_resources SET kv_store='sqlite' WHERE site_id=$1 AND name=$2 AND kind='kv' AND kv_store='postgres'`, n.SiteID, n.Name)
	if err != nil {
		return err
	}
	if changed, err := r.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("marker update: rows=%d: %v", changed, err)
	}
	r, err = tx.ExecContext(ctx, `DELETE FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2`, n.SiteID, n.Name)
	if err != nil {
		return err
	}
	if changed, err := r.RowsAffected(); err != nil || changed != count {
		return fmt.Errorf("source delete: rows=%d, copied=%d: %v", changed, count, err)
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// KVMoveRollback proves the restored rows inside the transaction before changing
// authority. A failed cast or comparison leaves the namespace entirely on SQLite.
func KVMoveRollback(ctx context.Context, db *sql.DB, n KVNamespace, entries []KVMoveEntry, compare func([]KVMoveEntry) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var store string
	var hasRules bool
	err = tx.QueryRowContext(ctx, `SELECT kv_store,rules IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name=$2 AND kind='kv' FOR UPDATE`, n.SiteID, n.Name).Scan(&store, &hasRules)
	if err != nil {
		return err
	}
	if hasRules {
		return fmt.Errorf("rollback refuses a namespace with saved rules; remove its rules before moving it to Postgres")
	}
	var count int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2`, n.SiteID, n.Name).Scan(&count); err != nil {
		return err
	}
	if store != "sqlite" || count != 0 {
		return fmt.Errorf("rollback requires SQLite marker and no Postgres rows")
	}
	for _, e := range entries {
		_, err = tx.ExecContext(ctx, `INSERT INTO site_storage_kv(site_id,resource_name,key,value,writer_id,created_at,updated_at) VALUES($1,$2,$3,$4::jsonb,$5,$6,$7)`, n.SiteID, n.Name, e.Key, e.Value, e.Writer, e.Created, e.Updated)
		if err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value::text,writer_id,created_at,updated_at FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2`, n.SiteID, n.Name)
	if err != nil {
		return err
	}
	var restored []KVMoveEntry
	for rows.Next() {
		var e KVMoveEntry
		if err = rows.Scan(&e.Key, &e.Value, &e.Writer, &e.Created, &e.Updated); err != nil {
			rows.Close()
			return err
		}
		restored = append(restored, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = compare(restored); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE site_storage_resources SET kv_store='postgres' WHERE site_id=$1 AND name=$2 AND kind='kv' AND kv_store='sqlite'`, n.SiteID, n.Name)
	if err != nil {
		return err
	}
	if changed, err := r.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("rollback marker update: rows=%d: %v", changed, err)
	}
	return tx.Commit()
}

// KVMoveState also works after a concurrent resource deletion (exists=false).
func KVMoveState(ctx context.Context, db *sql.DB, n KVNamespace) (store string, exists bool, rows int64, err error) {
	current, exists, rows, err := KVMoveCurrent(ctx, db, n)
	return current.Store, exists, rows, err
}

// KVMoveCurrent observes marker, location and source rows in one statement.
func KVMoveCurrent(ctx context.Context, db *sql.DB, n KVNamespace) (current KVNamespace, exists bool, rows int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT r.site_id,r.name,r.kv_store,s.user_id,s.name,s.deleted_at IS NOT NULL,
	(SELECT count(*) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2)
	FROM site_storage_resources r JOIN sites s ON s.id=r.site_id WHERE r.site_id=$1 AND r.name=$2 AND r.kind='kv'`, n.SiteID, n.Name).Scan(&current.SiteID, &current.Name, &current.Store, &current.OwnerID, &current.SiteName, &current.Deleted, &rows)
	if err == sql.ErrNoRows {
		return KVNamespace{}, false, 0, nil
	}
	return current, err == nil, rows, err
}
