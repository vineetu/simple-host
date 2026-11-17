package workers

import (
	"context"
	"errors"
	"reflect"
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

func TestEveryImmediateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	Every(ctx, 0, time.Hour, func(context.Context) { calls++; cancel() })
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	r := NewRunner(NewTracker("stopped"))
	r.Stop()
	r.Go(context.Background(), func(context.Context) { t.Error("started after Stop") })
	r.Wait()
}

type namedOwner struct{ names []string }

func (namedOwner) Start(context.Context) {}
func (namedOwner) Stop()                 {}
func (namedOwner) Wait()                 {}
func (o namedOwner) Status() []Status {
	var out []Status
	for _, name := range o.names {
		out = append(out, Status{Name: name})
	}
	return out
}

func TestSetOrder(t *testing.T) {
	var set Set
	if len(set.Order()) != 0 {
		t.Fatal("empty set has names")
	}
	set.Add(namedOwner{[]string{"z", "y"}})
	disabled := []string{"d", "c"}
	set.AddDisabled(disabled...)
	set.Add(namedOwner{[]string{"a"}})
	disabled[0] = "changed"
	want := []string{"y", "z", "d", "c", "a"}
	got := set.Order()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
	got[0] = "changed"
	if !reflect.DeepEqual(set.Order(), want) {
		t.Fatal("Order exposes its stored names")
	}
	var sorted []string
	for _, status := range set.Status() {
		sorted = append(sorted, status.Name)
	}
	if !reflect.DeepEqual(sorted, []string{"a", "c", "d", "y", "z"}) {
		t.Fatalf("Status names = %v", sorted)
	}
}
