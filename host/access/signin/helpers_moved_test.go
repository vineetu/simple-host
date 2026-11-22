package signin

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// acctZipFiles reads a .zip into name -> body.
func acctZipFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip (%d bytes): %v", len(b), err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

// acctFileUnder returns the archive entry whose name ends in suffix.
func acctFileUnder(t *testing.T, files map[string]string, suffix string) string {
	t.Helper()
	for name, body := range files {
		if strings.HasSuffix(name, suffix) {
			return body
		}
	}
	t.Fatalf("archive has no %s", suffix)
	return ""
}
