package storage

import (
	"context"
	"runtime"
	"time"

	runtimeStorage "github.com/vsriram/simple-host/internal/storage"
)

// SQLiteGate limits concurrent SQLite work.
type SQLiteGate struct {
	Slots chan struct{}
	wait  time.Duration
}

// NewSQLiteGate creates a gate with the given capacity and wait time.
func NewSQLiteGate(n int, wait time.Duration) *SQLiteGate {
	if n == 0 {
		n = runtime.NumCPU()
	}
	return &SQLiteGate{Slots: make(chan struct{}, n), wait: wait}
}

// Acquire waits for a slot and returns its release function.
func (g *SQLiteGate) Acquire(ctx context.Context) (func(), error) {
	wait, cancel := context.WithTimeout(ctx, g.wait)
	defer cancel()
	select {
	case g.Slots <- struct{}{}:
		return func() { <-g.Slots }, nil
	case <-wait.Done():
		return nil, runtimeStorage.ErrSQLiteBusy
	}
}
