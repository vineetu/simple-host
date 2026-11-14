package papermetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
	platform "github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/workers"
)

// Collector owns the product's measurements while sharing the platform's
// provenance, database, progress tracker and measurement primitives.
type Collector struct {
	platform  *platform.Collector
	db        *sql.DB
	dataDir   string
	apexHosts map[string]bool
	tracker   *workers.Tracker
	hack      *hackMetrics
	exports   platform.ProductExports
}

var _ platform.ProductCollector = (*Collector)(nil)

func (c *Collector) Snapshot(ctx context.Context, now time.Time) error {
	return c.hackSnapshot(ctx, now)
}

// Refresh drains phase notices and refreshes the metadata snapshot.
func (c *Collector) Refresh(ctx context.Context)    { c.refreshHackBackground(ctx) }
func (c *Collector) RefreshSignal() <-chan struct{} { return c.hack.refresh }

// Flush persists the completed Hack slots.
func (c *Collector) Flush(ctx context.Context, now time.Time) { c.flushHack(ctx, now) }
func (c *Collector) Histories(class int) []*platform.ClassHist {
	histories := make([]*platform.ClassHist, 0, len(c.hack.hist))
	for shard := range c.hack.hist {
		histories = append(histories, &c.hack.hist[shard][class])
	}
	return histories
}
func (c *Collector) RunDefinition() (string, int, string) {
	return "hack", MetricDefVersion, BucketScheme
}

// Exports is read for every exported cell, so it is built once at Install.
func (c *Collector) Exports() platform.ProductExports { return c.exports }

func (c *Collector) buildExports() platform.ProductExports {
	return platform.ProductExports{
		Sets: map[string]platform.ExportSet{
			"daily": c.setDaily, "runs": c.setRuns,
			"hack_events": c.setHackEvents, "hack_minutes": c.setHackMinutes,
			"hack_latency": c.setHackLatency, "hack_phases": c.setHackPhases,
		},
		Summary: map[string]any{"metric_def_version": MetricDefVersion, "bucket_schemes": map[string]string{"latency_buckets": LegacyBucketScheme, "hack_latency": BucketScheme}},
		Annotate: func(t *platform.Table) {
			t.MetricDefVersion = MetricDefVersion
			if t.Set != "daily" && t.Set != "runs" && !strings.HasPrefix(t.Set, "hack_") {
				t.MetricDefVersion = 1
			}
			if t.Set == "latency_buckets" || t.Set == "latency" {
				t.BucketScheme = LegacyBucketScheme
			}
			if t.Set == "hack_latency" {
				t.BucketScheme = BucketScheme
			}
		},
		Decode: func(column string, value []byte) (any, bool, error) {
			if column == "event_key" {
				return string(value), true, nil
			}
			if column == "counts" || column == "arrivals_1s" || column == "writes_1s" {
				var array pq.Int64Array
				if err := array.Scan(value); err != nil {
					return nil, true, err
				}
				return []int64(array), true, nil
			}
			return nil, false, nil
		},
		CSV: func(v any) string {
			if v == nil {
				return ""
			}
			if value, ok := v.([]int64); ok {
				encoded, _ := json.Marshal(value)
				return string(encoded)
			}
			return fmt.Sprint(v)
		},
	}
}

// Shared measurement types retain the same representation across the move.
type Table = platform.Table
type SiteFootprint = platform.SiteFootprint
type ClassHist = platform.ClassHist
type StatusWriter = platform.StatusWriter
type LogHistogram = platform.LogHistogram

const (
	NumClasses         = platform.NumClasses
	ClassNone          = platform.ClassNone
	ClassMCPStream     = platform.ClassMCPStream
	ClassAPIRead       = platform.ClassAPIRead
	ClassAPIWrite      = platform.ClassAPIWrite
	ClassDataWrite     = platform.ClassDataWrite
	MetricDefVersion   = platform.MetricDefVersion
	LegacyBucketScheme = platform.LegacyBucketScheme
	BucketScheme       = platform.BucketScheme
)

var classNames = append([]string{""}, platform.ClassNames()...)

func ToInt(v any) int64 { return platform.ToInt(v) }

// Export delegates the shared admin contract and response encoding.
func (c *Collector) Export(who platform.Who) http.Handler { return c.platform.Export(who) }
