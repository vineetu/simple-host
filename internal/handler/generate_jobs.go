package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// Direct-backend generations run as background jobs.
//
// A full page build is one long provider call — measured at ~77s — and answering
// it inline means the browser holds a connection that carries no bytes for that
// entire time. Mobile browsers drop such a connection well before the server is
// done, so the build would succeed, nginx would log 200, and the user would still
// be looking at "Cannot reach the server". The agent backend never had this
// problem because it answered with a job id and let the client poll; the direct
// backend now does the same, which is also the shape the client already handles.
const (
	// Long enough for the slowest full-page build, short enough that a wedged
	// provider call cannot pin a goroutine and its HTML forever. The client polls
	// for 9 minutes, so this stays inside its patience.
	jobRunTimeout = 8 * time.Minute
	// A finished job lingers this long so a slow poll still collects its answer.
	jobRetention = 10 * time.Minute
	// Ceilings so a scripted caller cannot fan out unbounded goroutines or hold
	// unbounded HTML in memory. Per-user is checked first, so one caller cannot
	// starve everyone else out of the global budget.
	maxJobsPerUser = 3
	maxJobsTotal   = 64
)

// generateJob is one direct-backend turn. Fields are guarded by jobStore.mu
// rather than a per-job lock: every access already goes through the store, so
// one lock is both sufficient and easier to reason about.
type generateJob struct {
	owner   string // user ID; a job is only ever visible to the user who started it
	started time.Time
	ended   time.Time
	done    bool
	reply   string
	html    string
	failure string // user-facing message, already mapped from the provider error
}

// jobStatusResponse is the poll body. The client keys off Status exactly:
// "done" collects reply+html, "error" surfaces Error, anything else means keep
// polling — so these strings are part of the wire contract.
type jobStatusResponse struct {
	Status string `json:"status"` // "running" | "done" | "error"
	Reply  string `json:"reply,omitempty"`
	HTML   string `json:"html,omitempty"`
	Error  string `json:"error,omitempty"`
}

type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*generateJob
}

func newJobStore() *jobStore {
	return &jobStore{jobs: make(map[string]*generateJob)}
}

// errJobsBusy is returned when the in-flight ceilings are hit.
var errJobsBusy = errors.New("too many builds already running")

// start registers a job owned by owner and runs work in the background, then
// returns the job id to poll. work receives a context with its own deadline —
// deliberately NOT the request context, which is cancelled the moment the
// handler returns and would kill every build instantly.
func (s *jobStore) start(owner string, work func(context.Context) (string, string, error), onErr func(error) string) (string, error) {
	id, err := newJobID()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	running, mine := 0, 0
	for _, j := range s.jobs {
		if j.done {
			continue
		}
		running++
		if j.owner == owner {
			mine++
		}
	}
	if mine >= maxJobsPerUser || running >= maxJobsTotal {
		s.mu.Unlock()
		return "", errJobsBusy
	}
	s.jobs[id] = &generateJob{owner: owner, started: time.Now()}
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobRunTimeout)
		defer cancel()
		reply, html, err := runJobWork(ctx, work)

		s.mu.Lock()
		defer s.mu.Unlock()
		j, ok := s.jobs[id]
		if !ok {
			return // swept while running; nothing left to record
		}
		j.done, j.ended = true, time.Now()
		if err != nil {
			j.failure = onErr(err)
			return
		}
		j.reply, j.html = reply, html
	}()

	return id, nil
}

// get returns the job if it exists and belongs to owner. A job belonging to
// someone else reports as missing rather than forbidden, so polling cannot be
// used to probe which ids exist.
func (s *jobStore) get(id, owner string) (generateJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.owner != owner {
		return generateJob{}, false
	}
	return *j, true
}

// startCleanup periodically evicts finished jobs past their retention window,
// and any job whose run should long since have timed out (belt and braces: the
// context deadline already bounds the goroutine).
func (s *jobStore) startCleanup(every time.Duration) {
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			s.mu.Lock()
			for id, j := range s.jobs {
				switch {
				case j.done && now.Sub(j.ended) > jobRetention:
					delete(s.jobs, id)
				case !j.done && now.Sub(j.started) > jobRunTimeout+time.Minute:
					delete(s.jobs, id)
				}
			}
			s.mu.Unlock()
		}
	}()
}

// runJobWork runs work and turns a panic into an ordinary error.
//
// This is load-bearing, not defensive habit. While a turn ran inside the HTTP
// handler it sat under net/http's per-connection recover, so a panic cost one
// request. Out here in our own goroutine nothing recovers, and a panic would
// take the whole process down with it — every hosted site, the API, the admin
// UI, and every other in-flight build. Model output reaches parsing code that
// does index arithmetic, so this is reachable from user input, not theoretical.
func runJobWork(ctx context.Context, work func(context.Context) (string, string, error)) (reply, html string, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("generate job: panic: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("generate job panicked: %v", r)
		}
	}()
	return work(ctx)
}

func newJobID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
