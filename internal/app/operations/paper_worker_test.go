package operations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/testdb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPaperOffWritesNothing(t *testing.T) {
	d, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	t.Setenv("PAPER_METRICS", "off")
	collector := papermetrics.New(d, "")
	c := NewPaperMetrics(collector)
	c.Start(context.Background())
	t.Cleanup(func() { c.Stop(); c.Wait() })
	product := &paperProduct{signal: make(chan struct{}, 1)}
	collector.SetProduct(product)
	collector.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if product.wrapped {
		t.Fatal("disabled collector wrapped product")
	}

	if err := collector.RegisterRun(context.Background()); err == nil {
		t.Fatal("registered disabled run")
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM paper_run`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("off wrote %d runs: %v", count, err)
	}
}

// paperProduct exercises the worker contract without product imports or tables.
type paperProduct struct {
	papermetrics.ProductCollector
	signal        chan struct{}
	refreshed     chan struct{}
	flushContext  context.Context
	flushDeadline time.Time
	snapshotErr   error
	snapshots     int
	wrapped       bool
}

func (p *paperProduct) Wrap(next http.Handler) http.Handler      { p.wrapped = true; return next }
func (p *paperProduct) DailyMetrics() []papermetrics.DailyMetric { return nil }
func (p *paperProduct) Snapshot(context.Context, time.Time) error {
	p.snapshots++
	return p.snapshotErr
}
func (p *paperProduct) Refresh(context.Context) {
	if p.refreshed != nil {
		p.refreshed <- struct{}{}
	}
}
func (p *paperProduct) RefreshSignal() <-chan struct{} { return p.signal }
func (p *paperProduct) Flush(ctx context.Context, _ time.Time) {
	p.flushContext = ctx
	p.flushDeadline, _ = ctx.Deadline()
	if ctx.Err() != nil {
		panic("final flush received canceled context")
	}
}
func TestPaperProductFinalFlush(t *testing.T) {
	product := &paperProduct{signal: make(chan struct{}, 1), refreshed: make(chan struct{}, 2)}
	collector := papermetrics.New(nil, "")
	collector.SetProduct(product)
	worker := NewPaperMetrics(collector)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); worker.productLoop(ctx) }()
	for i := 0; i < 2; i++ {
		select {
		case <-product.refreshed:
		case <-time.After(5 * time.Second):
			t.Fatal("product refresh not called")
		}
		if i == 0 {
			product.signal <- struct{}{}
		}
	}
	stopped := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("product loop did not stop")
	}
	if product.flushContext == nil || product.flushDeadline.Before(stopped.Add(19*time.Second)) || product.flushDeadline.After(stopped.Add(21*time.Second)) {
		t.Fatalf("final flush deadline %v", product.flushDeadline)
	}
	if product.flushContext.Err() != context.Canceled {
		t.Fatal("final flush context not released")
	}
}
func TestPaperProductSnapshotErrorAndNil(t *testing.T) {
	for _, withProduct := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil", true: "error"}[withProduct], func(t *testing.T) {
			d, err := sql.Open("postgres", testdb.Fresh(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			collector := papermetrics.New(d, "")
			product := &paperProduct{snapshotErr: errors.New("product snapshot unavailable")}
			if withProduct {
				collector.SetProduct(product)
			} else if collector.Product() != nil {
				t.Fatal("unexpected product")
			}
			worker := NewPaperMetrics(collector)
			worker.snapshots(context.Background())
			if withProduct {
				if product.snapshots != 1 || !strings.Contains(worker.tracker.Snapshot().LastError, "product snapshot unavailable") {
					t.Fatalf("snapshot result: %+v", worker.tracker.Snapshot())
				}
			} else if product.snapshots != 0 {
				t.Fatal("nil product invoked")
			}
		})
	}
}
