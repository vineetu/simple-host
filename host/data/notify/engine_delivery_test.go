package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/edition"
)

// An idle queue is polled once a tick, not once per worker; a backlog still
// fans out to several workers and is drained.
func TestDeliveryPollsOnceATickAndFansOut(t *testing.T) {
	const workers = 20
	e := NewEngine(nil, nil, edition.Limits{NotifyWorkers: workers}, "")
	ctx, cancel := context.WithCancel(context.Background())
	var polls, backlog, busy, most atomic.Int64
	e.runDelivery(ctx, workers, 10*time.Millisecond, func(context.Context) bool {
		polls.Add(1)
		if backlog.Add(-1) < 0 {
			backlog.Add(1)
			return false
		}
		n := busy.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		busy.Add(-1)
		return true
	})
	defer func() { cancel(); e.runner.Wait() }()

	time.Sleep(200 * time.Millisecond) // about 20 ticks
	if n := polls.Load(); n == 0 || n > 40 {
		t.Fatalf("idle: %d polls in 20 ticks, want about one a tick (20 workers each ticking would be ~400)", n)
	}

	backlog.Store(100)
	e.signal()
	deadline := time.Now().Add(5 * time.Second)
	for backlog.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if left := backlog.Load(); left != 0 {
		t.Fatalf("backlog not drained: %d left", left)
	}
	if most.Load() < 2 {
		t.Fatalf("a backlog ran on %d worker at a time, want it spread", most.Load())
	}
}
