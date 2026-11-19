package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
)

// InstallLockKey is the session advisory lock that ties a database to the one
// process family allowed to use it: every serving program holds it shared, and
// migrate-rules --apply and --revert take it exclusive. The maintenance lock file
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

// TxQuerier is implemented by the pool for read-only modes and by the locked
// connection for a maintenance command that changes saved data.
type TxQuerier interface {
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
