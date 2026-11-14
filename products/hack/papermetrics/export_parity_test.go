package papermetrics

import (
	"bytes"
	platform "github.com/vsriram/simple-host/internal/papermetrics"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExportOriginMain compares response bytes recorded before D2-S10a from
// origin/main 45fcb2be9d030899b1d41c31377e406b49928043 on the same seed.sql.
// There is one footprint/event row because their export order is random.
func TestExportOriginMain(t *testing.T) {
	d := isolatedDB(t)
	dir := "testdata/hack"
	seed, err := os.ReadFile(filepath.Join(dir, "seed.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(string(seed)); err != nil {
		t.Fatal(err)
	}
	c := Install(platform.New(d, ""))
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := file.Name()
		format := strings.TrimPrefix(filepath.Ext(name), ".")
		if format != "json" && format != "csv" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			set := strings.TrimSuffix(name, "."+format)
			r := httptest.NewRequest("GET", "/?set="+set+"&format="+format+"&from=2026-10-01&to=2026-10-01", nil)
			w := httptest.NewRecorder()
			c.Export(func(*http.Request) (bool, bool) { return true, true }).ServeHTTP(w, r)
			got := append([]byte(w.Result().Status+"\n"+w.Header().Get("Content-Type")+"\n"+w.Header().Get("Content-Disposition")+"\n"), w.Body.Bytes()...)
			want, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("export changed\ngot %s\nwant %s", got, want)
			}
		})
	}
}
