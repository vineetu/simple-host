package datatest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

// Download my data (GET /v1/me/export.zip) and Delete my account
// (DELETE /v1/me), end to end. Needs DB_DSN (host/db/schema.sql applied).

func tarFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a gzip (%d bytes): %v", len(b), err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[hdr.Name] = string(body)
	}
	return out
}
