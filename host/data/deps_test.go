package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

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
