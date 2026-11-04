package operations

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/testdb"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPaperOffWritesNothing(t *testing.T) {
	d, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	t.Setenv("PAPER_METRICS", "off")
	collector := papermetrics.New(d, "").WithHack()
	c := NewPaperMetrics(collector)
	c.Start(context.Background())
	t.Cleanup(func() { c.Stop(); c.Wait() })
	collector.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		papermetrics.RequestRecorder{}.RecordHackStage(r.Context(), "ignored", "open")
		papermetrics.RequestRecorder{}.HackPhasesChanged(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if err := collector.RegisterRun(context.Background()); err == nil {
		t.Fatal("registered disabled run")
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM paper_run`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("off wrote %d runs: %v", count, err)
	}
}
