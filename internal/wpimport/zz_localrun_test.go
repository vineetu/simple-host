package wpimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRun(t *testing.T) {
	xmlPath := os.Getenv("WPRUN_XML")
	if xmlPath == "" {
		t.Skip()
	}
	f, _ := os.Open(xmlPath)
	in := Input{Site: "test-site", WXR: []io.Reader{f}, Media: map[string]MediaFile{}}
	if m := os.Getenv("WPRUN_MEDIA"); m != "" {
		mf, _ := os.Open(m)
		var r io.Reader = mf
		if strings.HasSuffix(m, "gz") {
			r, _ = gzip.NewReader(mf)
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			b, _ := io.ReadAll(tr)
			name := h.Name
			in.Media[name] = MediaFile{Size: int64(len(b)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }}
		}
	}
	res, err := Convert(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("WPRUN_OUT")
	os.RemoveAll(out)
	for p, b := range res.Files {
		fp := filepath.Join(out, p)
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, b, 0o644)
	}
	j, _ := json.MarshalIndent(res.Report, "", "  ")
	os.WriteFile(filepath.Join(out, "_report.json"), j, 0o644)
}
