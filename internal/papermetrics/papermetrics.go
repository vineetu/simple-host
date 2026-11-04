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
	runner     *workers.Runner
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
	c := &Collector{runner: workers.NewRunner(t), tracker: t, db: db, dataDir: dataDir, apexHosts: map[string]bool{}, started: time.Now()}
	for _, h := range apexHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			c.apexHosts[h] = true
			c.apexHosts["www."+h] = true
		}
	}
	return c
}

// Start runs the flush, sample and snapshot loops until ctx ends.
func (c *Collector) Start(ctx context.Context) {
	if !Enabled() {
		return
	}
	c.runner.Go(ctx, func(ctx context.Context) {
		c.tracker.Attempt()
		newRun := c.runID.Load() == 0
		if _, err := c.ensureRun(ctx); err != nil {
			c.tracker.Fail("register run", err)
		} else if newRun {
			c.tracker.Progress()
		}
		if c.hack != nil {
			c.runner.Go(ctx, c.hackLoop)
		}
		c.loop(ctx)
	})
}

func (c *Collector) Stop()                    { c.runner.Stop() }
func (c *Collector) Wait()                    { c.runner.Wait() }
func (c *Collector) Status() []workers.Status { return c.runner.Status() }

func (c *Collector) loop(ctx context.Context) {
	c.tracker.Attempt()
	flush := time.NewTicker(time.Minute)
	sampleT := time.NewTicker(5 * time.Minute)
	snap := time.NewTicker(time.Hour)
	defer flush.Stop()
	defer sampleT.Stop()
	defer snap.Stop()
	c.takeSample(ctx) // primes the CPU baselines
	c.snapshots(ctx)
	for {
		select {
		case <-ctx.Done():
			c.flush(context.Background())
			return
		case <-flush.C:
			c.tracker.Attempt()
			c.flush(ctx)
		case <-sampleT.C:
			c.tracker.Attempt()
			c.takeSample(ctx)
		case <-snap.C:
			c.tracker.Attempt()
			c.snapshots(ctx)
		}
	}
}

// snapshots writes yesterday's daily snapshot and this week's footprint once
// each; both are no-ops when the row already exists, so restarts are safe.
func (c *Collector) snapshots(ctx context.Context) {
	c.tracker.Attempt()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := c.dailySnapshot(ctx, time.Now().UTC()); err != nil {
		c.tracker.Fail("daily snapshot", err)
	}
	if c.hack != nil {
		if err := c.hackSnapshot(ctx, time.Now().UTC()); err != nil {
			c.tracker.Fail("Hack snapshot", err)
		}
	}
	if err := c.weeklyFootprint(ctx, time.Now().UTC()); err != nil {
		c.tracker.Fail("footprint", err)
	}
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
