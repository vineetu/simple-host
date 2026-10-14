package wpimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if os.Getenv("WPRUN_FETCH") == "1" {
		client := &http.Client{Timeout: 20 * time.Second}
		in.Fetch = func(ctx context.Context, raw string) ([]byte, error) {
			u, _ := url.Parse(raw)
			theme, _ := url.Parse(os.Getenv("WPRUN_THEME"))
			okHost := u != nil && (strings.HasSuffix(u.Hostname(), ".wp.com") || theme != nil && u.Hostname() == theme.Hostname() || strings.HasPrefix(u.Hostname(), "fonts.g"))
			if !okHost {
				return nil, fmt.Errorf("local helper only fetches WordPress.com, the theme host and fonts")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", "SimpleHostImporter/1.0 (+https://simple-host.app)")
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return nil, fmt.Errorf("proxy returned %d", resp.StatusCode)
			}
			return io.ReadAll(io.LimitReader(resp.Body, DefaultLimits.MaxMediaFileSize+1))
		}
	}
	in.ThemeURL = os.Getenv("WPRUN_THEME")
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
