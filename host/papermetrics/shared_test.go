package papermetrics

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
)

func TestLogHistogramMerge(t *testing.T) {
	var all, left, right LogHistogram
	samples := make([]int64, 20000)
	for i := range samples {
		samples[i] = int64(math.Exp2(float64(i%300)/8)) + int64(i%7)
		all.Observe(samples[i])
		if i%2 == 0 {
			left.Observe(samples[i])
		} else {
			right.Observe(samples[i])
		}
	}
	a := all.Snapshot()
	var mergedLog LogHistogram
	mergedLog.Merge(left.Snapshot())
	mergedLog.Merge(right.Snapshot())
	merged := mergedLog.Snapshot()
	if a.N != merged.N || a.SumUS != merged.SumUS || a.MaxUS != merged.MaxUS || a.Offset != merged.Offset || fmt.Sprint(a.Counts) != fmt.Sprint(merged.Counts) {
		t.Fatal("merge differs from one histogram")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for _, q := range []float64{.5, .9, .99, .999} {
		actual := float64(samples[int(math.Ceil(float64(len(samples))*q))-1])
		estimate := merged.Quantile(q)
		if math.Abs(estimate/actual-1) > .044 {
			t.Fatalf("q=%v actual=%v estimate=%v", q, actual, estimate)
		}
	}
	for exponent := 1; exponent < 43; exponent++ {
		value := int64(1) << exponent
		if got := logBucket(value); got != exponent*8 {
			t.Fatalf("power of two %d => %d", value, got)
		}
	}
}
func TestRunConfigWhitelist(t *testing.T) {
	a := configHash(map[string]any{"person_hosts": "off", "DB_DSN": "secret-one", "ADMIN_API_KEY": "secret"})
	b := configHash(map[string]any{"person_hosts": "off", "DB_DSN": "different-secret"})
	if a != b || a == configHash(map[string]any{"person_hosts": "on"}) {
		t.Fatal("config whitelist")
	}
}

func TestRunLimitsPreserveOtherConfiguration(t *testing.T) {
	limits := map[string]any{"platform": 7, "product": 20}
	c := (&Collector{}).WithRunConfig(map[string]any{
		"limits": "old", "events": "hosted", "write_auth_mode": "strict",
		"person_hosts": "off", "site_hosts": "canonical", "paper_metrics": true,
		"ADMIN_API_KEY": "test-only-excluded",
	}).WithRunLimits(limits)
	want := map[string]any{
		"limits": limits, "events": "hosted", "write_auth_mode": "strict",
		"person_hosts": "off", "site_hosts": "canonical", "paper_metrics": true,
	}
	if !reflect.DeepEqual(c.runConfig, want) {
		t.Fatalf("run configuration = %v, want %v", c.runConfig, want)
	}
}
