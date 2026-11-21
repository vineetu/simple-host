package sites

import (
	"testing"
	"time"
)

// The queue estimate: room now means one timer interval; a full daily cap
// means waiting until the oldest issue of the day leaves the window.
func TestEstimateCertReady(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	l := siteCertLimits{Budget: 40, Daily: 12, PerRun: 6, Every: 10 * time.Minute, RetryIn: 6 * time.Hour}
	if got := estimateCertReady(nil, 0, now, l); got != now.Add(10*time.Minute) {
		t.Fatalf("empty queue: %s", got)
	}
	var day []time.Time
	for i := 0; i < 12; i++ {
		day = append(day, now.Add(-time.Duration(20-i)*time.Hour))
	}
	// First of the 12 was 20 h ago: room again in about 4 h.
	got := estimateCertReady(day, 0, now, l)
	if d := got.Sub(now); d < 4*time.Hour || d > 4*time.Hour+15*time.Minute {
		t.Fatalf("daily cap full: ready in %s", d)
	}
	// Three ahead: the fourth slot opens when the fourth-oldest leaves (17 h ago -> 7 h).
	got = estimateCertReady(day, 3, now, l)
	if d := got.Sub(now); d < 7*time.Hour || d > 7*time.Hour+15*time.Minute {
		t.Fatalf("three ahead: ready in %s", d)
	}
	// Weekly budget spent: waits for the week window.
	var week []time.Time
	for i := 0; i < 40; i++ {
		week = append(week, now.Add(-6*24*time.Hour-time.Duration(i)*time.Minute))
	}
	got = estimateCertReady(week, 0, now, l)
	if d := got.Sub(now); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("weekly budget spent: ready in %s", d)
	}
	// A huge queue and a long log answer quickly, and the same as four
	// weeks' budget.
	var long []time.Time
	for i := 0; i < 5000; i++ {
		long = append(long, now.Add(-30*24*time.Hour-time.Duration(i)*time.Minute))
	}
	start := time.Now()
	capped := estimateCertReady(long, 4*l.Budget, now, l)
	if huge := estimateCertReady(long, 100000, now, l); !huge.Equal(capped) || time.Since(start) > 2*time.Second {
		t.Fatalf("long queue: %s vs %s in %s", huge, capped, time.Since(start))
	}
}
