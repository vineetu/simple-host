package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/host/testdb"

	"github.com/vsriram/simple-host/host/papermetrics"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// testDisk is a site tree in a temporary directory.
func testDisk(t *testing.T) *runtime.DiskStorage {
	t.Helper()
	disk, err := runtime.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

// commitCounter counts the committed writes paper metrics hear of.
type commitCounter struct {
	papermetrics.Recorder
	n *int
}

func (c commitCounter) WriteCommitted(context.Context) { *c.n++ }

// countCommits is ctx with a recorder that counts committed writes into n.
func countCommits(ctx context.Context, n *int) context.Context {
	return papermetrics.WithRecorder(ctx, commitCounter{n: n})
}

// jsonBody is v as a JSON request body.
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// watchedReader is an empty body that records whether it was read.
type watchedReader struct{ read *bool }

func (r watchedReader) Read([]byte) (int, error) { *r.read = true; return 0, io.EOF }

// fatalReader is a body that must never be read.
type fatalReader struct{ t *testing.T }

func (r fatalReader) Read([]byte) (int, error) {
	r.t.Fatal("request body was read")
	return 0, io.EOF
}

// codeOf is the refusal's or the failure's code ("" when there is neither).
func codeOf(auth *Error, err error) string {
	if err == nil && auth != nil {
		err = auth
	}
	if err == nil || reflect.ValueOf(err).IsNil() {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return err.Error()
}

// coreDB is the test's database (DB_DSN); the test skips without one.
func coreDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMain(m *testing.M) { testdb.Main(m) }
