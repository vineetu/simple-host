package static

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// Pages builds the existing page dispatcher over one immutable routing copy.
// On a missing address it calls retry once, before writing a response.
type Pages func(model.SiteTable, http.Handler, func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler

func NewHandler(copy *Reloader, api http.Handler, pages Pages, apiRequest ...func(*http.Request) bool) http.Handler {
	pageAPI := pageHandoff(api)
	var mu sync.Mutex
	var snapshot *Snapshot
	var current http.Handler
	var serve func(http.ResponseWriter, *http.Request, bool)
	serve = func(w http.ResponseWriter, r *http.Request, retried bool) {
		if len(apiRequest) > 0 && apiRequest[0](r) {
			api.ServeHTTP(w, r)
			return
		}
		// These paths always belong to the API, even before the first load.
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/mcp" {
			api.ServeHTTP(w, r)
			return
		}
		s := copy.Current()
		if s == nil {
			if r.URL.Path == "/internal/tls-ask" {
				http.Error(w, "no", http.StatusForbidden)
			} else {
				Unavailable(w, r)
			}
			return
		}
		mu.Lock()
		if snapshot != s {
			current = pages(s.Table, pageAPI, func(w http.ResponseWriter, r *http.Request, address model.RoutingAddress) bool {
				if r.Context().Value(retryKey{}) == true {
					return false
				}
				_, _ = copy.Lookup(r.Context(), address)
				serve(w, r, true)
				return true
			})
			snapshot = s
		}
		h := current
		mu.Unlock()
		if retried {
			r = r.WithContext(context.WithValue(r.Context(), retryKey{}, true))
		}
		h.ServeHTTP(w, r)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(w, r, false) })
}

type retryKey struct{}

// Serve drains HTTP before stopping the reloader. A signal is a successful
// exit, including when the ten-second request drain deadline is reached.
func Serve(ctx context.Context, server *http.Server, copy *Reloader) error {
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); copy.Run(workerCtx) }()
	defer func() { cancel(); <-done }()
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		return nil
	}
}
