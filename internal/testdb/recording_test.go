package testdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/lib/pq"
)

func TestMain(m *testing.M) { Main(m) }
func TestRecordingNames(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want bool
	}{{"FROM widgets", true}, {`FROM "widgets"`, true}, {"FROM PUBLIC.WIDGETS", true}, {`FROM "public"."widgets"`, true}, {`FROM "public".WiDgEtS`, true}, {`FROM PuBlIc."widgets"`, true}, {`FROM "PUBLIC".widgets`, false}, {`FROM "public"."Widgets"`, false}, {`FROM "other".widgets`, false}, {`FROM otherpublic.widgets`, false}, {"n.widgets", false}, {`n."widgets"`, false}, {"site_widgets", false}, {"widgets_more", false}, {"mywidgets", false}, {"widgets.slug", true}} {
		if got := recordingNames(tt.s, "widgets"); got != tt.want {
			t.Errorf("%q: %v", tt.s, got)
		}
	}
	r := &Recorder{}
	r.record("SELECT * FROM widgets")
	copy := r.Statements()
	copy[0] = "changed"
	if !r.Names("WIDGETS") {
		t.Fatal("copy changed recorder")
	}
	r.Reset()
	if len(r.Statements()) != 0 {
		t.Fatal("reset")
	}
}
func TestRecordingLazyError(t *testing.T) {
	db, _ := OpenRecording("postgres://%zz")
	defer db.Close()
	if db.Ping() == nil {
		t.Fatal("expected invalid DSN error")
	}
}
func TestRecordingConcurrent(t *testing.T) {
	r := &Recorder{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.record("SELECT 1")
				_ = r.Statements()
			}
		}()
	}
	wg.Wait()
	if len(r.Statements()) != 800 {
		t.Fatal(len(r.Statements()))
	}
}
func TestRecordingDB(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN unset")
	}
	db, r := OpenRecording(os.Getenv("DB_DSN"))
	defer db.Close()
	ctx := context.Background()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	create := "CREATE TEMP TABLE the_temp_table (nums integer[])"
	insert := "INSERT INTO the_temp_table VALUES ($1)"
	prepared := "INSERT INTO the_temp_table VALUES (ARRAY[42])"
	query := "SELECT nums FROM the_temp_table"
	for _, s := range []string{create} {
		if _, e := tx.ExecContext(ctx, s); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := tx.ExecContext(ctx, insert, pq.Array([]int{1, 2})); e != nil {
		t.Fatal(e)
	}
	stmt, e := tx.PrepareContext(ctx, prepared)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e := stmt.ExecContext(ctx); e != nil {
			t.Fatal(e)
		}
	}
	stmt.Close()
	rows, e := tx.QueryContext(ctx, query)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var v pq.Int64Array
		if e := rows.Scan(&v); e != nil {
			t.Fatal(e)
		}
	}
	if e := rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	want := []string{"BEGIN", create, insert, prepared, prepared, query, "COMMIT"}
	if got := r.Statements(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if len(r.Naming("the_temp_table")) != 5 {
		t.Fatal(r.Statements())
	}
	if r.Names("temp_table") {
		t.Fatal("unexpected name")
	}
	r.Reset()
	tx, e = db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e := tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(r.Statements(), []string{"BEGIN", "ROLLBACK"}) {
		t.Fatal(r.Statements())
	}
	r.Reset()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := db.ExecContext(ctx, "SELECT 1"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if len(r.Statements()) != 8 {
		t.Fatal(r.Statements())
	}
	r.Reset()
	if _, e := db.ExecContext(ctx, "SELECT 1 AS the_temp_table_suffix, 1 AS other"); e != nil {
		t.Fatal(e)
	}
	if r.Names("the_temp_table") {
		t.Fatal(r.Statements())
	}
	r.record("SELECT alias.the_temp_table")
	if r.Names("the_temp_table") {
		t.Fatal(r.Statements())
	}
}

// A fast-path ErrSkip falls back to preparation without duplicating the text.
func TestRecordingErrSkip(t *testing.T) {
	r := &Recorder{}
	base := &recordingFakeConn{}
	db := sql.OpenDB(&recordingFakeConnector{conn: &recordingConn{Conn: base, r: r}})
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "SELECT * FROM widgets"); err != nil {
		t.Fatal(err)
	}
	if got := r.Statements(); !reflect.DeepEqual(got, []string{"SELECT * FROM widgets"}) {
		t.Fatal(got)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT * FROM widget_parts")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if got := r.Statements(); !reflect.DeepEqual(got, []string{"SELECT * FROM widgets", "SELECT * FROM widget_parts"}) {
		t.Fatal(got)
	}
	base.err = driver.ErrBadConn
	r.Reset()
	c := &recordingConn{Conn: base, r: r}
	if _, err := c.ExecContext(context.Background(), "failure", nil); err != driver.ErrBadConn {
		t.Fatal(err)
	}
	if len(r.Statements()) != 0 {
		t.Fatal(r.Statements())
	}
}

type recordingFakeConnector struct{ conn driver.Conn }

func (c *recordingFakeConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c *recordingFakeConnector) Driver() driver.Driver                        { return &pq.Driver{} }

type recordingFakeConn struct{ err error }

func (c *recordingFakeConn) Prepare(string) (driver.Stmt, error) { return &recordingFakeStmt{}, nil }
func (c *recordingFakeConn) Close() error                        { return nil }
func (c *recordingFakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *recordingFakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if c.err != nil {
		return nil, c.err
	}
	return nil, driver.ErrSkip
}
func (c *recordingFakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, driver.ErrSkip
}

type recordingFakeStmt struct{}

func (*recordingFakeStmt) Close() error  { return nil }
func (*recordingFakeStmt) NumInput() int { return -1 }
func (*recordingFakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (*recordingFakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return &recordingFakeRows{}, nil
}

type recordingFakeRows struct{}

func (*recordingFakeRows) Columns() []string         { return nil }
func (*recordingFakeRows) Close() error              { return nil }
func (*recordingFakeRows) Next([]driver.Value) error { return io.EOF }

// database/sql retries the failed operation on a fresh connection.
func TestRecordingBadConnRetry(t *testing.T) {
	for _, operation := range []string{"exec", "query", "begin", "stmt exec", "stmt query"} {
		t.Run(operation, func(t *testing.T) {
			r := &Recorder{}
			c := &recordingRetryConnector{r: r}
			db := sql.OpenDB(c)
			defer db.Close()
			q := "SELECT * FROM widgets"
			var err error
			switch operation {
			case "exec":
				_, err = db.Exec(q)
			case "query":
				var rows *sql.Rows
				rows, err = db.Query(q)
				if rows != nil {
					rows.Close()
				}
			case "begin":
				var tx *sql.Tx
				tx, err = db.Begin()
				if tx != nil {
					defer tx.Rollback()
				}
				q = "BEGIN"
			default:
				var stmt *sql.Stmt
				stmt, err = db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				if operation == "stmt exec" {
					_, err = stmt.Exec()
				} else {
					var rows *sql.Rows
					rows, err = stmt.Query()
					if rows != nil {
						rows.Close()
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Statements(); !reflect.DeepEqual(got, []string{q}) {
				t.Fatal(got)
			}
			if c.count != 2 {
				t.Fatalf("connections: %d", c.count)
			}
		})
	}
}

type recordingRetryConnector struct {
	r     *Recorder
	count int
}

func (c *recordingRetryConnector) Driver() driver.Driver { return &pq.Driver{} }
func (c *recordingRetryConnector) Connect(context.Context) (driver.Conn, error) {
	c.count++
	var err error
	if c.count == 1 {
		err = fmt.Errorf("retry: %w", driver.ErrBadConn)
	}
	return &recordingConn{Conn: &recordingRetryConn{err: err}, r: c.r}, nil
}

type recordingRetryConn struct{ err error }

func (c *recordingRetryConn) Close() error { return nil }
func (c *recordingRetryConn) Prepare(string) (driver.Stmt, error) {
	return &recordingRetryStmt{err: c.err}, nil
}
func (c *recordingRetryConn) Begin() (driver.Tx, error) { return &recordingFakeTx{}, c.err }
func (c *recordingRetryConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *recordingRetryConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), c.err
}
func (c *recordingRetryConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &recordingFakeRows{}, c.err
}
func (c *recordingRetryConn) Exec(string, []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), c.err
}
func (c *recordingRetryConn) Query(string, []driver.Value) (driver.Rows, error) {
	return &recordingFakeRows{}, c.err
}

type recordingFakeTx struct{}

func (*recordingFakeTx) Commit() error   { return nil }
func (*recordingFakeTx) Rollback() error { return nil }

type recordingRetryStmt struct{ err error }

func (*recordingRetryStmt) Close() error  { return nil }
func (*recordingRetryStmt) NumInput() int { return -1 }
func (s *recordingRetryStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), s.err
}
func (s *recordingRetryStmt) Query([]driver.Value) (driver.Rows, error) {
	return &recordingFakeRows{}, s.err
}
func (s *recordingRetryStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return s.Exec(nil)
}
func (s *recordingRetryStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return s.Query(nil)
}

func TestRecordingCopy(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		r := &Recorder{}
		c := &recordingConn{Conn: &recordingRetryConn{}, r: r}
		q := " \n cOpY widgets FROM STDIN"
		var stmt driver.Stmt
		var err error
		if contextual {
			stmt, err = c.PrepareContext(context.Background(), q)
		} else {
			stmt, err = c.Prepare(q)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Statements(); !reflect.DeepEqual(got, []string{q}) {
			t.Fatal(got)
		}
		for i := 0; i < 3; i++ {
			if contextual {
				_, err = stmt.(driver.StmtExecContext).ExecContext(context.Background(), nil)
			} else {
				_, err = stmt.Exec(nil)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := r.Statements(); !reflect.DeepEqual(got, []string{q}) {
			t.Fatal(got)
		}
	}
}
func TestRecordingNamedFallback(t *testing.T) {
	r := &Recorder{}
	s := &recordingStmt{Stmt: &recordingFakeStmt{}, query: "SELECT 1", r: r}
	args := []driver.NamedValue{{Name: "value", Value: 1}}
	_, execErr := s.ExecContext(context.Background(), args)
	_, queryErr := s.QueryContext(context.Background(), args)
	for _, err := range []error{execErr, queryErr} {
		if err == nil || err.Error() != "sql: driver does not support the use of Named Parameters" {
			t.Fatal(err)
		}
	}
	if len(r.Statements()) != 0 {
		t.Fatal(r.Statements())
	}
}

func TestRecordingRetryCleanupAllMethods(t *testing.T) {
	for _, failure := range []error{driver.ErrSkip, fmt.Errorf("wrapped: %w", driver.ErrBadConn)} {
		r := &Recorder{}
		c := &recordingConn{Conn: &recordingRetryConn{err: failure}, r: r}
		stmt := &recordingStmt{Stmt: &recordingRetryStmt{err: failure}, query: "SELECT * FROM widgets", r: r}
		ctx := context.Background()
		calls := []func() error{
			func() error { _, e := c.Exec("query", nil); return e },
			func() error { _, e := c.Query("query", nil); return e },
			func() error { _, e := c.ExecContext(ctx, "query", nil); return e },
			func() error { _, e := c.QueryContext(ctx, "query", nil); return e },
			func() error { _, e := c.Begin(); return e },
			func() error { _, e := c.BeginTx(ctx, driver.TxOptions{}); return e },
			func() error { _, e := stmt.Exec(nil); return e },
			func() error { _, e := stmt.Query(nil); return e },
			func() error { _, e := stmt.ExecContext(ctx, nil); return e },
			func() error { _, e := stmt.QueryContext(ctx, nil); return e },
		}
		for i, call := range calls {
			if err := call(); !errors.Is(err, failure) {
				t.Fatalf("method %d: %v", i, err)
			}
			if got := r.Statements(); len(got) != 0 {
				t.Fatalf("method %d: %v", i, got)
			}
		}
	}
}

// Exercise the driver's context prepare branch as well as its legacy fallback.
func (c *recordingRetryConn) PrepareContext(_ context.Context, q string) (driver.Stmt, error) {
	return c.Prepare(q)
}

func TestRecordingArgumentInterfaces(t *testing.T) {
	for _, v := range []any{&recordingConn{}, &recordingStmt{}} {
		if _, ok := v.(driver.NamedValueChecker); ok {
			t.Errorf("%T exposes NamedValueChecker", v)
		}
		if _, ok := v.(driver.ColumnConverter); ok {
			t.Errorf("%T exposes ColumnConverter", v)
		}
	}
}
