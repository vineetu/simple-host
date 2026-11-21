package servertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// tarGzOf is a gzipped tar holding index.html plus a file of n zero bytes:
// a few KB on the wire, n bytes unpacked.
func TarGzOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
