package servertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TarNames(t *testing.T, b []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	return names
}
