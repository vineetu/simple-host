package storage

import (
	"context"
	"errors"
	"runtime"
	"time"
)

// ErrSQLiteBusy indicates that a slot could not be acquired in time.
var ErrSQLiteBusy = errors.New("storage is busy")

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
		return nil, ErrSQLiteBusy
	}
}
