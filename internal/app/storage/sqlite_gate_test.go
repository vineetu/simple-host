package storage

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func checkAdmissionIdle(t *testing.T, slots *SiteSlots) {
	t.Helper()
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if len(slots.sites) != 0 || len(slots.gate.Slots) != 0 {
		t.Fatalf("leaked slots: sites=%d global=%d", len(slots.sites), len(slots.gate.Slots))
	}
}

func TestSiteSlotsSerializeAndIndependentSites(t *testing.T) {
	slots := NewSiteSlots(1, NewSQLiteGate(2, time.Second), time.Second)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- slots.Admit(context.Background(), "a", func(context.Context, *Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var ran atomic.Bool
	sameDone := make(chan error, 1)
	go func() {
		sameDone <- slots.Admit(ctx, "a", func(context.Context, *Admission) error { ran.Store(true); return nil })
	}()
	if err := slots.Admit(context.Background(), "b", func(context.Context, *Admission) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-sameDone; err == nil || err.(*Error).Code != "storage_busy" || ran.Load() {
		t.Fatalf("same site: %v, ran=%v", err, ran.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkAdmissionIdle(t, slots)
}

func TestSiteSlotsCancellationAndSharedWait(t *testing.T) {
	slots := NewSiteSlots(1, NewSQLiteGate(1, time.Second), 80*time.Millisecond)
	// A global waiter holds its site's slot; cancellation frees it.
	release, err := slots.gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := slots.Admit(ctx, "a", func(context.Context, *Admission) error { t.Fatal("cancelled call ran"); return nil }); err == nil {
		t.Fatal("expected cancellation")
	}
	start := time.Now()
	err = slots.Admit(context.Background(), "a", func(context.Context, *Admission) error { t.Fatal("full gate admitted"); return nil })
	if err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("gate wait: %v (%v)", err, time.Since(start))
	}
	release()
	checkAdmissionIdle(t, slots)
	// A successful wait must not cancel the operation context.
	if err := slots.Admit(context.Background(), "a", func(ctx context.Context, _ *Admission) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
}

func TestSiteSlotsCombinedAdmissionDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const budget = 80 * time.Millisecond
		slots := NewSiteSlots(1, NewSQLiteGate(1, time.Second), budget)
		// Occupy both admission stages independently so releasing the site
		// slot leaves the global gate blocked.
		site := &siteSlot{slots: make(chan struct{}, 1)}
		site.slots <- struct{}{}
		slots.sites["a"] = site
		release, err := slots.gate.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		start := time.Now()
		go func() {
			done <- slots.Admit(context.Background(), "a", func(context.Context, *Admission) error {
				return errors.New("blocked operation ran")
			})
		}()
		synctest.Wait()
		time.Sleep(60 * time.Millisecond)
		<-site.slots
		synctest.Wait() // Admit now holds the site slot and waits for the gate.
		if len(site.slots) != 1 {
			t.Fatal("waiter did not acquire the released site slot")
		}
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		select {
		case err := <-done:
			if e, ok := err.(*Error); !ok || e.Code != "storage_busy" {
				t.Fatalf("combined admission: %v", err)
			}
		default:
			t.Fatal("global wait exceeded the original admission deadline")
		}
		if elapsed := time.Since(start); elapsed != budget {
			t.Fatalf("admission took %v, want %v", elapsed, budget)
		}
		release()
		checkAdmissionIdle(t, slots)
	})
}

func TestSiteSlotsRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, p := range []any{"sqlite3: out of memory", sqlite3.NOMEM, fmt.Errorf("wrapped: %w", sqlite3.NOMEM), errors.New("other"), []int{1}} {
			slots := NewSiteSlots(1, NewSQLiteGate(1, time.Second), time.Second)
			err := slots.Admit(context.Background(), "a", func(_ context.Context, a *Admission) error {
				if committed {
					a.Committed()
				}
				panic(p)
			})
			if committed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want := "storage_failed"
				switch v := p.(type) {
				case string:
					if v == "sqlite3: out of memory" {
						want = "query_too_expensive"
					}
				case error:
					if errors.Is(v, sqlite3.NOMEM) {
						want = "query_too_expensive"
					}
				}
				if err == nil || err.(*Error).Code != want {
					t.Fatalf("panic %T: %v, want %s", p, err, want)
				}
			}
			checkAdmissionIdle(t, slots)
		}
	}
	slots := NewSiteSlots(1, NewSQLiteGate(1, time.Second), time.Second)
	err := slots.Admit(context.Background(), "a", func(context.Context, *Admission) error { return fmt.Errorf("wrapped: %w", sqlite3.NOMEM) })
	if err == nil || err.(*Error).Code != "query_too_expensive" {
		t.Fatal(err)
	}
	checkAdmissionIdle(t, slots)
}

func TestSiteSlotsCapacity(t *testing.T) {
	slots := NewSiteSlots(4, NewSQLiteGate(4, time.Second), time.Second)
	entered, release, done := make(chan struct{}, 4), make(chan struct{}), make(chan error, 4)
	for range 4 {
		go func() {
			done <- slots.Admit(context.Background(), "a", func(context.Context, *Admission) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	for range 4 {
		<-entered
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := slots.Admit(ctx, "a", func(context.Context, *Admission) error { return nil }); err == nil {
		t.Fatal("fifth admitted")
	}
	close(release)
	for range 4 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	checkAdmissionIdle(t, slots)
}
