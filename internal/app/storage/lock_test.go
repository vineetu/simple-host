package storage

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWriteLockDeadline(t *testing.T) {
	s := New(nil, Deps{Locks: &sync.Map{}})
	for _, timeout := range []time.Duration{time.Second, 2 * time.Second} {
		ctx, release, err := s.writeLock(context.Background(), "site", time.Second, timeout, nil)
		if err != nil {
			t.Fatal(err)
		}
		deadline, ok := ctx.Deadline()
		left := time.Until(deadline)
		release()
		if !ok || left < timeout-100*time.Millisecond {
			t.Fatalf("deadline %v, want %v", left, timeout)
		}
	}
}
