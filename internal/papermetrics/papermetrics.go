// Package papermetrics keeps the long-running, aggregate-only numbers the
// research papers on this deployment need ("The Thin Server", SIM-335):
// server-side latency per route class, process and box resource samples, a
// daily snapshot of counts, and a weekly footprint row per site with no
// identifier at all. Methodology: docs/paper-metrics.md.
//
// Exported tables store no person, address, site name or path. The private
// Hack key lookup alone holds an operational event ID, deleted with that event.
//
// Cost is the point of the paper, so it has to be near zero here too: a
// Host request uses classHist (BenchmarkWrap); Hack adds event-slot atomics
// and a member set (BenchmarkHackWrap and BenchmarkHackWrapParallel). The
// database sees completed slot batches and a process sample every five minutes.
// A missing table only loses metrics; it never stops the server.
package papermetrics

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vsriram/simple-host/internal/workers"
)

// Enabled reports whether PAPER_METRICS allows collection (default on).
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PAPER_METRICS")))
	return v != "off" && v != "false" && v != "0"
}

// Collector owns every paper metric for one server process.
type Collector struct {
	tracker    *workers.Tracker
	db         *sql.DB
	dataDir    string
	apexHosts  map[string]bool
	started    time.Time
	hist       [numClasses]classHist
	lastSample sample
	hack       *hackMetrics
	runID      atomic.Int64
	runMu      sync.Mutex
	runConfig  map[string]any
	// commit is the build commit used for provenance (see WithCommit).
	commit string
}

// New makes a collector. apexHosts are the platform's own host names
// (simple-host.app, www.simple-host.app): a page there is a platform page,
// any other host is a hosted site.
func New(db *sql.DB, dataDir string, apexHosts ...string) *Collector {
	t := workers.NewTracker("operations.paper_metrics")
	c := &Collector{tracker: t, db: db, dataDir: dataDir, apexHosts: map[string]bool{}, started: time.Now()}
	for _, h := range apexHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.apexHosts[h] = true
			c.apexHosts["www."+h] = true
		}
	}
	return c
}

// ProgressTracker is shared with the owning operations loop.
func (c *Collector) ProgressTracker() *workers.Tracker { return c.tracker }

// InitializeRun registers provenance before collection begins.
func (c *Collector) InitializeRun(ctx context.Context) {
	c.tracker.Attempt()
	newRun := c.runID.Load() == 0
	if _, err := c.ensureRun(ctx); err != nil {
		c.tracker.Fail("register run", err)
	} else if newRun {
		c.tracker.Progress()
	}
}
func (c *Collector) HackEnabled() bool              { return c.hack != nil }
func (c *Collector) HackRefresh() <-chan struct{}   { return c.hack.refresh }
func (c *Collector) Flush(ctx context.Context)      { c.flush(ctx) }
func (c *Collector) TakeSample(ctx context.Context) { c.takeSample(ctx) }
func (c *Collector) DailySnapshot(ctx context.Context, now time.Time) error {
	return c.dailySnapshot(ctx, now)
}
func (c *Collector) HackSnapshot(ctx context.Context, now time.Time) error {
	return c.hackSnapshot(ctx, now)
}
func (c *Collector) WeeklyFootprint(ctx context.Context, now time.Time) error {
	return c.weeklyFootprint(ctx, now)
}

// Wrap times every request through h into its class's histogram.
func (c *Collector) Wrap(h http.Handler) http.Handler {
	if !Enabled() {
		return h
	}
	if c.hack != nil {
		return c.wrapHack(h)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cl := c.classify(r)
		if cl == classNone {
			h.ServeHTTP(w, r)
			return
		}
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		t0 := time.Now()
		h.ServeHTTP(rec, r)
		c.hist[cl].observe(time.Since(t0), rec.status, rec.bytes)
	})
}

// statusWriter records the status and body bytes, and keeps the fast paths
// of the writer it wraps: ReadFrom (sendfile for site files), Flush (MCP
// streams) and, via Unwrap, http.ResponseController.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		s.bytes += n
		return n, err
	}
	return io.Copy(writerOnly{s}, src)
}

// writerOnly hides ReadFrom so io.Copy does not call back into it.
type writerOnly struct{ io.Writer }

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// classHist is one class's in-memory counters since the last flush.
type classHist struct {
	buckets  [numBuckets]atomic.Int64
	requests atomic.Int64
	errors   atomic.Int64 // 5xx
	totalUS  atomic.Int64
	bytes    atomic.Int64
}

func (h *classHist) observe(d time.Duration, status int, bytes int64) {
	us := d.Microseconds()
	h.buckets[bucketFor(us)].Add(1)
	h.requests.Add(1)
	h.totalUS.Add(us)
	h.bytes.Add(bytes)
	if status >= 500 {
		h.errors.Add(1)
	}
}
