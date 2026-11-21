package notify

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/workers"
)

// Engine is the process's notification machinery: event intake, the
// delivery workers, the webhook and web-push senders, and the budgets that
// bound all of them. One per data handler.
type Engine struct {
	svc        *Service
	ed         *edition.Edition
	limits     edition.Limits
	siteDomain string
	client     client

	// minute/day cap the events stored per site; challenge bounds
	// verification sends per owner, host and address; perURL bounds sends
	// to one receiver; rest bounds the REST routes per client address.
	minute, day, challenge, perURL, rest *httpx.RateLimiter
	// pushSiteDay/pushPersonDay are web push's own daily budgets, per site
	// and per recipient, spent only by a real push attempt.
	pushSiteDay, pushPersonDay *httpx.RateLimiter

	ownersMu sync.Mutex
	owners   map[string]int
	wake     chan struct{}
	// emitting holds one token per event still being stored; verifying
	// bounds concurrent verification challenges and test sends.
	emitting, verifying chan struct{}

	start, stop sync.Once
	stopped     chan struct{}
	emitMu      sync.Mutex
	emitIdle    chan struct{}
	emitPending int
	dropped     atomic.Uint64
	// Both runners are joined before database shutdown; emits may precede Start.
	runner, emitRunner         *workers.Runner
	delivery, retention, emits *workers.Tracker

	push pushState
}

// NewEngine builds the engine for limits; siteDomain names the install in
// a web push's VAPID claims.
func NewEngine(database *sql.DB, ed *edition.Edition, l edition.Limits, siteDomain string) *Engine {
	perDay := func(n int) *httpx.RateLimiter { return httpx.NewRateLimiter(float64(n), float64(n)/86400) }
	e := &Engine{
		svc: New(database), ed: ed, limits: l, siteDomain: siteDomain,
		client: client{timeout: l.NotifyTimeout},
		minute: httpx.NewRateLimiter(float64(l.NotifyPerMinute), float64(l.NotifyPerMinute)/60), day: perDay(l.NotifyPerDay),
		challenge: httpx.NewRateLimiter(10, 10.0/60), rest: httpx.NewRateLimiter(60, 1),
		perURL: urlBudget(l), pushSiteDay: perDay(l.NotifyPushPerSitePerDay), pushPersonDay: perDay(l.NotifyPushPerPersonPerDay),
		owners: map[string]int{}, wake: make(chan struct{}, l.NotifyWorkers),
		emitting: make(chan struct{}, 64), verifying: make(chan struct{}, 10),
		stopped: make(chan struct{}), emitIdle: make(chan struct{}),
		delivery:  workers.NewTracker("notifications.delivery"),
		retention: workers.NewTracker("notifications.retention"),
		emits:     workers.NewTracker("notifications.emit"),
	}
	close(e.emitIdle)
	e.runner = workers.NewRunner(e.delivery, e.retention)
	e.emitRunner = workers.NewRunner(e.emits)
	return e
}

// urlBudget is the per-receiver send budget: a one-attempt burst keeps the
// first minute within the configured rate.
func urlBudget(l edition.Limits) *httpx.RateLimiter {
	return httpx.NewRateLimiter(1, float64(l.NotifyPerURLPerMinute)/60)
}

// RESTLimiter bounds the notification and push REST routes per address.
func (e *Engine) RESTLimiter() *httpx.RateLimiter { return e.rest }

func (e *Engine) acquireOwner(owner string) bool {
	e.ownersMu.Lock()
	defer e.ownersMu.Unlock()
	if e.owners[owner] >= e.limits.NotifyOwnerConcurrency {
		return false
	}
	e.owners[owner]++
	return true
}

func (e *Engine) releaseOwner(owner string) {
	e.ownersMu.Lock()
	defer e.ownersMu.Unlock()
	e.owners[owner]--
	if e.owners[owner] == 0 {
		delete(e.owners, owner)
	}
}

// signal wakes one delivery worker.
func (e *Engine) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Emit stores one event in the background: fn runs on its own context,
// after the caller has answered. A full queue drops the event (counted).
// Admission and the idle signal share a lock so shutdown cannot race a new
// Runner.Go against the drain.
func (e *Engine) Emit(fn func(context.Context)) {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	select {
	case <-e.stopped:
		return
	default:
	}
	select {
	case e.emitting <- struct{}{}:
	default:
		e.emits.Fail("enqueue event", fmt.Errorf("queue full, dropped=%d", e.dropped.Add(1)))
		return
	}
	if e.emitPending == 0 {
		e.emitIdle = make(chan struct{})
	}
	e.emitPending++
	e.emitRunner.Go(context.Background(), func(ctx context.Context) {
		defer e.emitDone()
		e.emits.Attempt()
		fn(ctx)
	})
}

func (e *Engine) emitDone() {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	<-e.emitting
	e.emitPending--
	if e.emitPending == 0 {
		close(e.emitIdle)
	}
}

// Start runs the delivery workers and the retention sweeper until ctx
// ends; env is read at each delivery. The first sweep finishes before any
// worker starts.
func (e *Engine) Start(ctx context.Context, env func() Env) {
	e.start.Do(func() {
		select {
		case <-e.stopped:
			return
		default:
		}
		e.runner.Go(ctx, func(ctx context.Context) { <-ctx.Done(); e.Stop() })
		ready := make(chan struct{})
		e.runner.Go(ctx, func(ctx context.Context) { e.Sweep(ctx, env().Mailer); close(ready) })
		select {
		case <-ready:
		case <-e.stopped:
			return
		case <-ctx.Done():
		}
		for i := 0; i < e.limits.NotifyWorkers; i++ {
			e.runner.Go(ctx, func(ctx context.Context) { e.deliverLoop(ctx, env) })
		}
		e.runner.Go(ctx, func(ctx context.Context) { e.sweepLoop(ctx, env) })
	})
}

// deliverLoop drains the queue each second and whenever an event arrives.
func (e *Engine) deliverLoop(ctx context.Context, env func() Env) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-e.wake:
		}
		for ctx.Err() == nil && e.Deliver(ctx, env()) {
		}
	}
}

// sweepLoop prunes history and idle budgets hourly.
func (e *Engine) sweepLoop(ctx context.Context, env func() Env) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.Sweep(ctx, env().Mailer)
			e.minute.EvictIdle(time.Hour)
			e.day.EvictIdle(48 * time.Hour)
			e.challenge.EvictIdle(time.Hour)
			e.perURL.EvictIdle(time.Hour)
			e.rest.EvictIdle(time.Hour)
		}
	}
}

// Stop ends intake, stops the workers and gives events already accepted
// their full five-second persistence budget before cancelling them.
func (e *Engine) Stop() {
	e.stop.Do(func() {
		e.emitMu.Lock()
		close(e.stopped)
		idle := e.emitIdle
		e.emitMu.Unlock()
		e.runner.Stop()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-idle:
		case <-timer.C:
		}
		e.emitRunner.Stop()
	})
}

// Wait blocks until every worker and every emit has returned.
func (e *Engine) Wait() { e.runner.Wait(); e.emitRunner.Wait() }

// Status reports the workers, with the emits still queued as pending.
func (e *Engine) Status() []workers.Status {
	e.emits.SetPending(int64(len(e.emitting)))
	return append(e.runner.Status(), e.emitRunner.Status()...)
}
