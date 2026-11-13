// Package workers provides background owner lifecycle and progress reporting.
package workers

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type Status struct {
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	Running      bool       `json:"running"`
	LastAttempt  *time.Time `json:"last_attempt"`
	LastProgress *time.Time `json:"last_progress"`
	LastError    string     `json:"last_error"`
	LastErrorAt  *time.Time `json:"last_error_at"`
	Pending      *int64     `json:"pending"`
}
type Tracker struct {
	mu     sync.Mutex
	status Status
}

func NewTracker(name string) *Tracker { return &Tracker{status: Status{Name: name}} }
func (t *Tracker) Attempt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.status.LastAttempt = &now
}
func (t *Tracker) Progress() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.status.LastProgress = &now
}
func (t *Tracker) Fail(op string, err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	now := time.Now()
	t.status.LastError = op + ": " + err.Error()
	t.status.LastErrorAt = &now
	t.mu.Unlock()
	log.Printf("worker %s: %s: %v", t.status.Name, op, err)
}
func (t *Tracker) SetPending(n int64) { t.mu.Lock(); defer t.mu.Unlock(); t.status.Pending = &n }
func (t *Tracker) Snapshot() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.status
	if s.LastAttempt != nil {
		v := *s.LastAttempt
		s.LastAttempt = &v
	}
	if s.LastProgress != nil {
		v := *s.LastProgress
		s.LastProgress = &v
	}
	if s.LastErrorAt != nil {
		v := *s.LastErrorAt
		s.LastErrorAt = &v
	}
	if s.Pending != nil {
		v := *s.Pending
		s.Pending = &v
	}
	return s
}

type Runner struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	stopped  bool
	active   int
	wg       sync.WaitGroup
	trackers []*Tracker
}

func NewRunner(trackers ...*Tracker) *Runner { return &Runner{trackers: trackers} }
func (r *Runner) Go(ctx context.Context, fn func(context.Context)) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if r.ctx == nil {
		r.ctx, r.cancel = context.WithCancel(ctx)
	}
	ctx = r.ctx
	r.active++
	r.wg.Add(1)
	r.mu.Unlock()
	go func() { defer r.wg.Done(); defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }(); fn(ctx) }()
}
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
}
func (r *Runner) Wait() { r.wg.Wait() }
func (r *Runner) Status() []Status {
	r.mu.Lock()
	running := r.active > 0
	r.mu.Unlock()
	out := make([]Status, 0, len(r.trackers))
	for _, t := range r.trackers {
		s := t.Snapshot()
		s.Enabled = true
		s.Running = running
		out = append(out, s)
	}
	return out
}
func Every(ctx context.Context, first, every time.Duration, fn func(context.Context)) {
	timer := time.NewTimer(first)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type Owner interface {
	Start(context.Context)
	Stop()
	Wait()
	Status() []Status
}
type Set struct {
	owners   []Owner
	disabled []Status
	order    []registration
}

// A registration is one Add or AddDisabled call, including multi-name owners.
type registration struct {
	owner    Owner
	disabled []string
}

func (s *Set) Add(o Owner) {
	s.owners = append(s.owners, o)
	s.order = append(s.order, registration{owner: o})
}
func (s *Set) AddDisabled(names ...string) {
	for _, name := range names {
		s.disabled = append(s.disabled, Status{Name: name})
	}
	s.order = append(s.order, registration{disabled: append([]string(nil), names...)})
}

// Order returns names in Add/AddDisabled registration order. A single owner can
// report several names; those are sorted because its Status may iterate a map.
// Disabled names keep the order supplied to AddDisabled.
func (s *Set) Order() []string {
	var out []string
	for _, entry := range s.order {
		if entry.owner == nil {
			out = append(out, entry.disabled...)
			continue
		}
		var names []string
		for _, status := range entry.owner.Status() {
			names = append(names, status.Name)
		}
		sort.Strings(names)
		out = append(out, names...)
	}
	return out
}

func (s *Set) Start(ctx context.Context) {
	for _, o := range s.owners {
		o.Start(ctx)
	}
}
func (s *Set) Stop() {
	for _, o := range s.owners {
		o.Stop()
	}
}
func (s *Set) Wait(ctx context.Context) []string {
	done := make([]chan struct{}, len(s.owners))
	for i, o := range s.owners {
		done[i] = make(chan struct{})
		go func(o Owner, ch chan struct{}) { o.Wait(); close(ch) }(o, done[i])
	}
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			var names []string
			for i, o := range s.owners {
				select {
				case <-done[i]:
				default:
					for _, st := range o.Status() {
						names = append(names, st.Name)
					}
				}
			}
			sort.Strings(names)
			return names
		}
	}
	return nil
}
func (s *Set) Status() []Status {
	out := append([]Status{}, s.disabled...)
	for _, o := range s.owners {
		out = append(out, o.Status()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
