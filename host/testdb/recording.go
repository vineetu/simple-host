// Recording connections let tests assert statement order or that a code path
// issues no statement naming a given table.
package testdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"

	"github.com/lib/pq"
)

type recordedStatement struct {
	id   uint64
	text string
}

// Recorder holds statements in execution order and is safe for concurrent use.
type Recorder struct {
	mu         sync.Mutex
	next       uint64
	statements []recordedStatement
}

func (r *Recorder) record(s string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	r.statements = append(r.statements, recordedStatement{r.next, s})
	return r.next
}
func (r *Recorder) skip(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.statements {
		if s.id == id {
			r.statements = append(r.statements[:i], r.statements[i+1:]...)
			return
		}
	}
}

// Statements returns a copy, including transaction boundaries.
func (r *Recorder) Statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.statements))
	for i, s := range r.statements {
		out[i] = s.text
	}
	return out
}

// Reset clears the recorded statements.
func (r *Recorder) Reset() { r.mu.Lock(); defer r.mu.Unlock(); r.statements = nil }

// Naming returns statements containing name as a whole SQL word, ignoring case.
func (r *Recorder) Naming(name string) []string {
	var out []string
	for _, s := range r.Statements() {
		if recordingNames(s, name) {
			out = append(out, s)
		}
	}
	return out
}

// Names reports whether any recorded statement names the object.
func (r *Recorder) Names(name string) bool { return len(r.Naming(name)) > 0 }
func recordingNames(s, n string) bool {
	original := s
	s, n = strings.ToLower(s), strings.ToLower(n)
	if n == "" {
		return false
	}
	ident := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' }
	for off := 0; off < len(s); {
		i := strings.Index(s[off:], n)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(n)
		left := i
		if i > 0 && s[i-1] == '"' && end < len(s) && s[end] == '"' {
			okQuoted := original[i:end] == n
			if !okQuoted {
				off = end
				continue
			}
			left--
		}
		ok := (i == 0 || !ident(s[i-1])) && (end == len(s) || !ident(s[end]))
		if left > 0 && s[left-1] == '.' {
			prefix := s[:left-1]
			schema := "public"
			if strings.HasSuffix(prefix, `"public"`) {
				schema = `"public"`
			}
			start := len(prefix) - len(schema)
			ok = ok && start >= 0 && prefix[start:] == schema &&
				(schema != `"public"` || original[start:left-1] == schema) &&
				(start == 0 || !ident(prefix[start-1]) && prefix[start-1] != '.')
		}
		if ok {
			return true
		}
		off = end
	}
	return false
}

type recordingConnector struct {
	underlying driver.Connector
	err        error
	recorder   *Recorder
}

// OpenRecording opens a lazy PostgreSQL connection pool and its statement log.
func OpenRecording(dsn string) (*sql.DB, *Recorder) {
	c, e := pq.NewConnector(dsn)
	r := &Recorder{}
	return sql.OpenDB(&recordingConnector{c, e, r}), r
}
func (c *recordingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.err != nil {
		return nil, c.err
	}
	conn, e := c.underlying.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &recordingConn{conn, c.recorder}, nil
}
func (c *recordingConnector) Driver() driver.Driver { return &pq.Driver{} }

type recordingConn struct {
	driver.Conn
	r *Recorder
}

func (c *recordingConn) Prepare(q string) (driver.Stmt, error) {
	s, e := c.Conn.Prepare(q)
	if e != nil {
		return nil, e
	}
	return c.stmt(s, q), nil
}
func (c *recordingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		s, e := p.PrepareContext(ctx, q)
		if e != nil {
			return nil, e
		}
		return c.stmt(s, q), nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return c.Prepare(q)
	}
}

// stmt wraps a prepared statement. A COPY is recorded once here, not per row.
func (c *recordingConn) stmt(s driver.Stmt, q string) driver.Stmt {
	isCopy := recordingCopy(q)
	if isCopy {
		c.r.record(q)
	}
	return &recordingStmt{Stmt: s, query: q, r: c.r, copy: isCopy}
}

// recorded records q, then runs it. A statement the driver did not run
// (ErrSkip, ErrBadConn: database/sql tries another path) is taken back out.
func recorded[T any](r *Recorder, q string, run func() (T, error)) (T, error) {
	id := r.record(q)
	v, e := run()
	if errors.Is(e, driver.ErrSkip) || errors.Is(e, driver.ErrBadConn) {
		r.skip(id)
	}
	return v, e
}

func (c *recordingConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	ex, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return recorded(c.r, q, func() (driver.Result, error) { return ex.ExecContext(ctx, q, a) })
}
func (c *recordingConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	ex, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return recorded(c.r, q, func() (driver.Rows, error) { return ex.QueryContext(ctx, q, a) })
}
func (c *recordingConn) Exec(q string, a []driver.Value) (driver.Result, error) {
	ex, ok := c.Conn.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	return recorded(c.r, q, func() (driver.Result, error) { return ex.Exec(q, a) })
}
func (c *recordingConn) Query(q string, a []driver.Value) (driver.Rows, error) {
	ex, ok := c.Conn.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	return recorded(c.r, q, func() (driver.Rows, error) { return ex.Query(q, a) })
}
func (c *recordingConn) Begin() (driver.Tx, error) {
	t, e := recorded(c.r, "BEGIN", c.Conn.Begin)
	if e != nil {
		return nil, e
	}
	return &recordingTx{t, c.r}, nil
}
func (c *recordingConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	b, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return c.Begin()
	}
	t, e := recorded(c.r, "BEGIN", func() (driver.Tx, error) { return b.BeginTx(ctx, o) })
	if e != nil {
		return nil, e
	}
	return &recordingTx{t, c.r}, nil
}
func (c *recordingConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}
func (c *recordingConn) ResetSession(ctx context.Context) error {
	if p, ok := c.Conn.(driver.SessionResetter); ok {
		return p.ResetSession(ctx)
	}
	return nil
}
func (c *recordingConn) IsValid() bool {
	if p, ok := c.Conn.(driver.Validator); ok {
		return p.IsValid()
	}
	return true
}

// Like pq v1.10.9, recordingConn and recordingStmt expose neither
// NamedValueChecker nor ColumnConverter, preserving database/sql's argument-checking path.

type recordingTx struct {
	driver.Tx
	r *Recorder
}

func (t *recordingTx) Commit() error   { t.r.record("COMMIT"); return t.Tx.Commit() }
func (t *recordingTx) Rollback() error { t.r.record("ROLLBACK"); return t.Tx.Rollback() }

type recordingStmt struct {
	driver.Stmt
	query string
	r     *Recorder
	copy  bool
}

func recordingCopy(q string) bool {
	fields := strings.Fields(q)
	return len(fields) > 0 && strings.EqualFold(fields[0], "COPY")
}

// runStmt records the statement once per execution; a COPY was recorded at prepare.
func runStmt[T any](s *recordingStmt, call func() (T, error)) (T, error) {
	if s.copy {
		return call()
	}
	return recorded(s.r, s.query, call)
}
func (s *recordingStmt) Exec(a []driver.Value) (driver.Result, error) {
	return runStmt(s, func() (driver.Result, error) { return s.Stmt.Exec(a) })
}
func (s *recordingStmt) Query(a []driver.Value) (driver.Rows, error) {
	return runStmt(s, func() (driver.Rows, error) { return s.Stmt.Query(a) })
}
func (s *recordingStmt) ExecContext(ctx context.Context, a []driver.NamedValue) (driver.Result, error) {
	if p, ok := s.Stmt.(driver.StmtExecContext); ok {
		return runStmt(s, func() (driver.Result, error) { return p.ExecContext(ctx, a) })
	}
	v, e := recordingValues(ctx, a)
	if e != nil {
		return nil, e
	}
	return s.Exec(v)
}
func (s *recordingStmt) QueryContext(ctx context.Context, a []driver.NamedValue) (driver.Rows, error) {
	if p, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return runStmt(s, func() (driver.Rows, error) { return p.QueryContext(ctx, a) })
	}
	v, e := recordingValues(ctx, a)
	if e != nil {
		return nil, e
	}
	return s.Query(v)
}
func recordingValues(ctx context.Context, a []driver.NamedValue) ([]driver.Value, error) {
	v := make([]driver.Value, len(a))
	for i, n := range a {
		if n.Name != "" {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		v[i] = n.Value
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return v, nil
}
