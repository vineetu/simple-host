package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// InstallLockKey is the session advisory lock that ties a database to the one
// process family allowed to use it: every serving program holds it shared, and
// kv-move --apply and --rollback take it exclusive. The maintenance lock file
// covers one data directory only, so without this a command pointed at a copy
// of the data directory could rewrite a database a live server is using from
// another directory. Any constant works as long as nothing else in this
// database uses it (the migrate lock is 7461_2026_0927).
const InstallLockKey = 7461_2026_1115

var (
	// ErrDatabaseInUse: a serving program (or another maintenance command)
	// holds the install lock on this database.
	ErrDatabaseInUse = errors.New("a server (or another maintenance command) is using this database; stop it first")
	// ErrDatabaseMaintenance: a maintenance command holds it exclusively.
	ErrDatabaseMaintenance = errors.New("a maintenance command is running on this database")
)

// InstallLock is a held install advisory lock. It lives on one dedicated
// connection; Postgres drops it when that connection ends, so a dead process
// never leaves it held.
type InstallLock struct {
	conn      *sql.Conn
	exclusive bool
}

// Conn is the session holding the lock. Maintenance queries and transactions
// must use it so a lost lock also prevents any further database changes.
func (l *InstallLock) Conn() *sql.Conn { return l.conn }

// OpenServingDB takes a shared install lock on every pool connection before it
// can execute application SQL. Reconnected request and worker connections must
// acquire the lock too, so maintenance suspends their database access even if
// the separate lifetime hold has disconnected and has not noticed yet.
func OpenServingDB(dsn string) (*sql.DB, error) {
	c, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(installConnector{c}), nil
}

type installConnector struct{ *pq.Connector }

func (c installConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.(driver.QueryerContext).QueryContext(ctx,
		`SELECT pg_try_advisory_lock_shared($1)`, []driver.NamedValue{{Ordinal: 1, Value: int64(InstallLockKey)}})
	if err != nil {
		conn.Close()
		return nil, err
	}
	values := make([]driver.Value, 1)
	err = rows.Next(values)
	closeErr := rows.Close()
	if err = errors.Join(err, closeErr); err != nil {
		conn.Close()
		return nil, err
	}
	if values[0] != true {
		conn.Close()
		return nil, ErrDatabaseMaintenance
	}
	return conn, nil
}

// KVMoveDB is implemented by the pool for read-only modes and by the locked
// connection for apply and rollback.
type KVMoveDB interface {
	sqlkit.Querier
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// LockInstall takes the install advisory lock without waiting: shared for a
// serving program, exclusive for a command that rewrites saved data.
func LockInstall(ctx context.Context, db *sql.DB, exclusive bool) (*InstallLock, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	query := `SELECT pg_try_advisory_lock_shared($1)`
	if exclusive {
		query = `SELECT pg_try_advisory_lock($1)`
	}
	var got bool
	if err = conn.QueryRowContext(ctx, query, InstallLockKey).Scan(&got); err != nil {
		conn.Close()
		return nil, err
	}
	if !got {
		conn.Close()
		if exclusive {
			return nil, ErrDatabaseInUse
		}
		return nil, ErrDatabaseMaintenance
	}
	return &InstallLock{conn: conn, exclusive: exclusive}, nil
}

// Alive reports whether the lock's connection still answers. A connection that
// ended (a database restart) has lost the lock.
func (l *InstallLock) Alive(ctx context.Context) bool {
	return l.conn.PingContext(ctx) == nil
}

// Close releases the lock and returns the connection. It is safe on a
// connection that has already ended.
func (l *InstallLock) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := `SELECT pg_advisory_unlock_shared($1)`
	if l.exclusive {
		query = `SELECT pg_advisory_unlock($1)`
	}
	// The unlock matters because the connection goes back to a pool; if it
	// fails the connection is discarded, which also releases the lock.
	_, err := l.conn.ExecContext(ctx, query, InstallLockKey)
	if err != nil {
		l.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return errors.Join(err, l.conn.Close())
}

// InstallID returns the identity saved in the database, or "" when no row
// exists. It never writes.
func InstallID(ctx context.Context, db sqlkit.Querier) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM install_identity`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// CreateInstallID saves id as the install's identity unless one is already
// there, and returns the identity now stored (another process may have won).
func CreateInstallID(ctx context.Context, db *sql.DB, id string) (string, error) {
	if _, err := db.ExecContext(ctx, `INSERT INTO install_identity(id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
		return "", err
	}
	return InstallID(ctx, db)
}

// KVNamespace includes the facts needed to resolve live and trashed directories.
type KVNamespace struct {
	SiteID, Name, Store, OwnerID, SiteName string
	Deleted                                bool
	// HasRules: the namespace has saved rules, which only the SQLite store enforces.
	HasRules bool
}

func KVMoveNamespaces(ctx context.Context, db KVMoveDB) ([]KVNamespace, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.site_id,r.name,r.kv_store,s.user_id,s.name,s.deleted_at IS NOT NULL,r.rules IS NOT NULL FROM site_storage_resources r JOIN sites s ON s.id=r.site_id WHERE r.kind='kv' ORDER BY r.site_id,r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KVNamespace
	for rows.Next() {
		var n KVNamespace
		if err := rows.Scan(&n.SiteID, &n.Name, &n.Store, &n.OwnerID, &n.SiteName, &n.Deleted, &n.HasRules); err != nil {
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
func KVMoveSnapshot(ctx context.Context, db KVMoveDB, n KVNamespace) ([]KVMoveEntry, int64, error) {
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
func KVMoveCommit(ctx context.Context, db KVMoveDB, n KVNamespace, count int64, beforeCommit func() error) error {
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

// ErrKVAlreadyRestored: the namespace is no longer marked sqlite, or Postgres
// already holds rows for it. Rollback has nothing to restore and changes nothing.
var ErrKVAlreadyRestored = errors.New("namespace already has its rows in Postgres")

// ErrKVHasRules marks a rollback refused because the namespace has saved rules.
var ErrKVHasRules = errors.New("rollback refuses a namespace with saved rules")

// KVRulesRefusal names the namespace a rollback refuses for its saved rules.
func KVRulesRefusal(n KVNamespace) error {
	return fmt.Errorf("%w: namespace %q of site %s has saved rules, and rules are enforced only on the SQLite store; it stays on SQLite", ErrKVHasRules, n.Name, n.SiteID)
}

// KVMoveRollback proves the restored rows inside the transaction before changing
// authority. A failed cast or comparison leaves the namespace entirely on SQLite.
func KVMoveRollback(ctx context.Context, db KVMoveDB, n KVNamespace, entries []KVMoveEntry, compare func([]KVMoveEntry) error) error {
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
		return KVRulesRefusal(n)
	}
	var count int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2`, n.SiteID, n.Name).Scan(&count); err != nil {
		return err
	}
	if store != "sqlite" || count != 0 {
		return ErrKVAlreadyRestored
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
func KVMoveState(ctx context.Context, db KVMoveDB, n KVNamespace) (store string, exists bool, rows int64, err error) {
	current, exists, rows, err := KVMoveCurrent(ctx, db, n)
	return current.Store, exists, rows, err
}

// KVMoveCurrent observes marker, location and source rows in one statement.
func KVMoveCurrent(ctx context.Context, db KVMoveDB, n KVNamespace) (current KVNamespace, exists bool, rows int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT r.site_id,r.name,r.kv_store,s.user_id,s.name,s.deleted_at IS NOT NULL,r.rules IS NOT NULL,
	(SELECT count(*) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2)
	FROM site_storage_resources r JOIN sites s ON s.id=r.site_id WHERE r.site_id=$1 AND r.name=$2 AND r.kind='kv'`, n.SiteID, n.Name).Scan(&current.SiteID, &current.Name, &current.Store, &current.OwnerID, &current.SiteName, &current.Deleted, &current.HasRules, &rows)
	if err == sql.ErrNoRows {
		return KVNamespace{}, false, 0, nil
	}
	return current, err == nil, rows, err
}
