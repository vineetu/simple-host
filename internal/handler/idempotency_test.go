package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

func createRequest(account, method, path, key, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if account != "" {
		r = r.WithContext(auth.WithUser(r.Context(), &db.User{ID: account}))
	}
	r.Header.Set("Idempotency-Key", key)
	return r
}

func TestCreateIdempotencyReplayAndConflict(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := newCreateIdempotencyStore()
			calls := 0
			h := store.wrap(NoticeMiddleware("9.0.0")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{"files":{}}` {
					t.Errorf("handler received %q, %v", body, err)
				}
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("{\"result\": 1}\n"))
			})))
			first := httptest.NewRecorder()
			r := createRequest("a", "POST", "/v1/sites/demo/files?create=1", "key", `{"files":{}}`)
			r.Header.Set("X-Skill-Version", "1.0.0")
			h.ServeHTTP(first, r)
			second := httptest.NewRecorder()
			h.ServeHTTP(second, createRequest("a", "POST", "/v1/sites/demo/files?create=1", "key", `{"files":{}}`))
			if calls != 1 || second.Code != status || first.Body.String() != second.Body.String() || second.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("replay: calls=%d, first=%d %s, second=%d %s", calls, first.Code, first.Body, second.Code, second.Body)
			}
			conflict := httptest.NewRecorder()
			h.ServeHTTP(conflict, createRequest("a", "POST", "/v1/sites/demo/files?create=1", "key", `{"files":{"index.html":"new"}}`))
			if calls != 1 || conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"idempotency_key_reused"`) {
				t.Fatalf("conflict: calls=%d, %d %s", calls, conflict.Code, conflict.Body)
			}
		})
	}
}

func TestCreateIdempotencyScopeAndExpiry(t *testing.T) {
	store := newCreateIdempotencyStore()
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	calls := 0
	h := store.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(201) }))
	for _, c := range []struct{ account, method, path, key string }{
		{"a", "POST", "/v1/sites/one/files", "k"},
		{"b", "POST", "/v1/sites/one/files", "k"},
		{"a", "POST", "/v1/sites/two/files", "k"},
		{"a", "POST", "/v1/sites/one/files?create=1", "k"},
		{"a", "POST", "/v1/sites/one/files", "other"},
		{"a", "POST", "/v1/sites/one/files", ""},
		{"a", "POST", "/v1/sites/one/files", ""},
		{"a", "GET", "/v1/sites/one/files", "k"},
		{"a", "GET", "/v1/sites/one/files", "k"},
		{"", "POST", "/v1/sites/one/files", "k"},
		{"", "POST", "/v1/sites/one/files", "k"},
	} {
		before := calls
		h.ServeHTTP(httptest.NewRecorder(), createRequest(c.account, c.method, c.path, c.key, "body"))
		if calls != before+1 {
			t.Fatalf("unexpected replay for %+v", c)
		}
	}
	now = now.Add(createIdempotencyTTL)
	h.ServeHTTP(httptest.NewRecorder(), createRequest("a", "POST", "/v1/sites/one/files", "k", "different"))
	if calls != 12 {
		t.Fatalf("expired entry not re-executed: %d", calls)
	}
	store.evictExpired()
	if len(store.entries) != 1 {
		t.Fatalf("cleanup left %d entries", len(store.entries))
	}
}

func TestCreateIdempotencyConcurrent(t *testing.T) {
	store := newCreateIdempotencyStore()
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	h := store.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		w.WriteHeader(201)
		_, _ = w.Write([]byte("created"))
	}))
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range responses {
		responses[i] = httptest.NewRecorder()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := createRequest("a", "POST", "/v1/sites/demo/files", "key", "body")
			r = r.WithContext(auth.WithUser(ctx, &db.User{ID: "a"}))
			h.ServeHTTP(responses[i], r)
		}(i)
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times", calls.Load())
	}
	for _, r := range responses {
		if r.Code != 201 || r.Body.String() != "created" {
			t.Fatalf("response %d %s", r.Code, r.Body)
		}
	}
}

func TestCreateIdempotencyStorageSiteScope(t *testing.T) {
	store := newCreateIdempotencyStore()
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	})
	for _, site := range []string{"site-one", "site-two"} {
		h := store.wrapAccount("visitor", site, next)
		for i := 0; i < 2; i++ {
			h.ServeHTTP(httptest.NewRecorder(), createRequest("", "POST", "/v1/sites/blog/storage/sqlite/db/tables/posts/rows", "key", "body"))
		}
	}
	if calls != 2 {
		t.Fatalf("same path on different resolved sites ran %d times, want 2", calls)
	}
}

func TestCreateIdempotencyRateLimitHeadersStayCurrent(t *testing.T) {
	store := newCreateIdempotencyStore()
	limiter := newRateLimiter(2, 0.5)
	limiter.now = func() time.Time { return time.Unix(0, 0) }
	calls := 0
	h := rateLimitByIP(limiter, store.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(201)
		_, _ = w.Write([]byte("created"))
	})))
	for i, remaining := range []string{"1", "0"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("a", "POST", "/v1/sites/demo/files", "key", "body"))
		if w.Code != 201 || w.Body.String() != "created" || w.Header().Get("RateLimit-Remaining") != remaining {
			t.Fatalf("request %d: %d %s %v", i, w.Code, w.Body, w.Header())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("a", "POST", "/v1/sites/demo/files", "key", "body"))
	if calls != 1 || w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("rate limited replay: calls=%d, %d %v", calls, w.Code, w.Header())
	}
}
