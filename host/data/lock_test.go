package data

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWriteLockDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{time.Second, 2 * time.Second} {
		s := New(nil, Deps{Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: timeout})
		ctx, release, err := s.WriteLock(context.Background(), Call{OwnerID: "owner", SiteName: "site"}, false)
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
