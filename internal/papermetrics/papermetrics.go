// Package papermetrics keeps the long-running, aggregate-only numbers the
// research papers on this deployment need ("The Thin Server", SIM-335):
// server-side latency per route class, process and box resource samples, a
// daily snapshot of counts, and a weekly footprint row per site with no
// identifier at all. Methodology: docs/paper-metrics.md.
//
// Nothing here stores a person, an address, a site name or a path. Every
// table is a count, a size, a duration, or a histogram bucket.
//
// Cost is the point of the paper, so it has to be near zero here too: a
// request costs one classification and two atomic adds (BenchmarkWrap), and
// the database sees one small upsert a minute plus one sample every five.
// A missing table only loses metrics; it never stops the server.
package papermetrics

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Enabled reports whether PAPER_METRICS allows collection (default on).
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PAPER_METRICS")))
	return v != "off" && v != "false" && v != "0"
}

// Collector owns every paper metric for one server process.
type Collector struct {
	db         *sql.DB
	dataDir    string
	apexHosts  map[string]bool
	started    time.Time
	hist       [numClasses]classHist
	lastSample sample
}

// New makes a collector. apexHosts are the platform's own host names
// (simple-host.app, www.simple-host.app): a page there is a platform page,
// any other host is a hosted site.
func New(db *sql.DB, dataDir string, apexHosts ...string) *Collector {
	c := &Collector{db: db, dataDir: dataDir, apexHosts: map[string]bool{}, started: time.Now()}
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
	go c.loop(ctx)
}

func (c *Collector) loop(ctx context.Context) {
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
			c.flush(ctx)
		case <-sampleT.C:
			c.takeSample(ctx)
		case <-snap.C:
			c.snapshots(ctx)
		}
	}
}

// snapshots writes yesterday's daily snapshot and this week's footprint once
// each; both are no-ops when the row already exists, so restarts are safe.
func (c *Collector) snapshots(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := c.dailySnapshot(ctx, time.Now().UTC()); err != nil {
		log.Printf("paper metrics daily snapshot: %v", err)
	}
	if err := c.weeklyFootprint(ctx, time.Now().UTC()); err != nil {
		log.Printf("paper metrics footprint: %v", err)
	}
}

// Wrap times every request through h into its class's histogram.
func (c *Collector) Wrap(h http.Handler) http.Handler {
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
