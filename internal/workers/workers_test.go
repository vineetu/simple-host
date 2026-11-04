package workers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	tr := NewTracker("test")
	tr.Attempt()
	tr.Progress()
	tr.Fail("scan", errors.New("failed"))
	tr.SetPending(2)
	s := tr.Snapshot()
	if s.LastAttempt == nil || s.LastProgress == nil || s.LastError != "scan: failed" || *s.Pending != 2 {
		t.Fatal(s)
	}
	r := NewRunner(tr)
	r.Go(context.Background(), func(ctx context.Context) {
		Every(ctx, time.Hour, time.Hour, func(context.Context) { t.Error("unexpected tick") })
	})
	r.Stop()
	r.Stop()
	r.Wait()
	if r.Status()[0].Running {
		t.Fatal("still running")
	}
}

type blocked struct{ release chan struct{} }

func (b *blocked) Start(context.Context) {}
func (b *blocked) Stop()                 {}
func (b *blocked) Wait()                 { <-b.release }
func (b *blocked) Status() []Status      { return []Status{{Name: "blocked"}} }
func TestSetDeadline(t *testing.T) {
	b := &blocked{make(chan struct{})}
	defer close(b.release)
	var s Set
	s.Add(b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	names := s.Wait(ctx)
	if len(names) != 1 || names[0] != "blocked" {
		t.Fatal(names)
	}
}
