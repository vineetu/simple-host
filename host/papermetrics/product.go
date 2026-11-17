package papermetrics

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// ProductMetrics is the product's lifecycle, owned by the paper worker.
// The final Flush runs with a fresh, bounded context after cancellation.
type ProductMetrics interface {
	Snapshot(context.Context, time.Time) error
	Refresh(context.Context)
	RefreshSignal() <-chan struct{}
	Flush(context.Context, time.Time)
}

// ProductCollector supplies the measurements and export policy of one product.
// It is installed before serving; nil keeps the platform's original behaviour.
type ProductCollector interface {
	ProductMetrics
	Wrap(http.Handler) http.Handler
	Histories(class int) []*ClassHist
	DailyMetrics() []DailyMetric
	Exports() ProductExports
	RunDefinition() (role string, version int, bucketScheme string)
}

type ExportSet func(context.Context, string, string, *http.Request) (Table, error)

type ProductExports struct {
	Sets     map[string]ExportSet
	Summary  map[string]any
	Annotate func(*Table)
	Decode   func(column string, value []byte) (any, bool, error)
	CSV      func(any) string
}

// DailyMetric keeps query execution in the existing snapshot order. A product
// may supply its own persistence; failures skip that metric, as before.
type DailyMetric struct {
	Name, Query string
	Store       func(context.Context, string, float64) error
}

func (c *Collector) SetProduct(p ProductCollector) { c.product = p }
func (c *Collector) Product() ProductMetrics       { return c.product }
func (c *Collector) DB() *sql.DB                   { return c.db }
func (c *Collector) DataDir() string               { return c.dataDir }
func (c *Collector) ApexHosts() map[string]bool {
	hosts := make(map[string]bool, len(c.apexHosts))
	for host, value := range c.apexHosts {
		hosts[host] = value
	}
	return hosts
}
