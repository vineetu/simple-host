package servertest

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// zipFiles reads a .zip into name -> body.
func ZipFiles(t *testing.T, b []byte) map[string]string {
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

// fileUnder returns the archive entry whose name ends in suffix.
func FileUnder(t *testing.T, files map[string]string, suffix string) string {
	t.Helper()
	for name, body := range files {
		if strings.HasSuffix(name, suffix) {
			return body
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	t.Fatalf("archive has no %s: %v", suffix, names)
	return ""
}
