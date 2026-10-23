package handler

import (
	"crypto/sha256"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
)

const createIdempotencyTTL = 24 * time.Hour

// Caps bound worst-case memory from an authenticated caller minting many
// distinct keys (including ones that only ever produce an error response,
// which are cached like any other). Hit either and new keys are refused
// before their handler runs; existing keys keep replaying normally.
const (
	createIdempotencyMaxPerAccount = 500
	createIdempotencyMaxTotal      = 50000
)

type createIdempotencyKey struct {
	account, route, key string
}

type createIdempotencyResponse struct {
	hash    [sha256.Size]byte
	done    chan struct{}
	expires time.Time
	status  int
	header  http.Header
	body    []byte
}

// Only response bytes and request hashes are retained. Like the rate limiter,
// this store is local to the single server process and is lost on restart.
type createIdempotencyStore struct {
	mu            sync.Mutex
	entries       map[createIdempotencyKey]*createIdempotencyResponse
	accountCounts map[string]int
	now           func() time.Time
}

func newCreateIdempotencyStore() *createIdempotencyStore {
	return &createIdempotencyStore{
		entries:       make(map[createIdempotencyKey]*createIdempotencyResponse),
		accountCounts: make(map[string]int),
		now:           time.Now,
	}
}

// deleteLocked removes k and keeps accountCounts consistent. Callers hold s.mu.
func (s *createIdempotencyStore) deleteLocked(k createIdempotencyKey) {
	if _, ok := s.entries[k]; !ok {
		return
	}
	delete(s.entries, k)
	if n := s.accountCounts[k.account] - 1; n > 0 {
		s.accountCounts[k.account] = n
	} else {
		delete(s.accountCounts, k.account)
	}
}

func (s *createIdempotencyStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.entries {
		if !v.expires.IsZero() && !s.now().Before(v.expires) {
			s.deleteLocked(k)
		}
	}
}

func (s *createIdempotencyStore) startCleanup() {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			s.evictExpired()
		}
	}()
}

// wrap belongs inside authentication and rate limiting, outside the response
// notice middleware, so authentication stays live and replayed bytes stay exact.
func (s *createIdempotencyStore) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user == nil {
			next.ServeHTTP(w, r)
			return
		}
		s.wrapAccount(user.ID, "", next).ServeHTTP(w, r)
	})
}

// Storage handlers already resolve their caller and resource permissions. They
// supply that identity and resolved site ID without changing their auth path.
func (s *createIdempotencyStore) wrapAccount(account, siteID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || key == "" || account == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Archives can be large: spool only for this request instead of keeping
		// another complete upload in RAM while the handler decodes it. Existing
		// per-account and per-route body limits still apply in the handler.
		body, err := os.CreateTemp("", "simple-host-idempotency-*")
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "could not read request body"})
			return
		}
		defer os.Remove(body.Name())
		defer body.Close()
		limit := largestSiteLimit()*4/3 + (1 << 20)
		if photoCap := int64(photoCountLimit*(photoLimit("PHOTO_MAX_BYTES", 1000000)+4096) + 4096); photoCap > limit {
			limit = photoCap
		}
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout(limit)))
		hash := sha256.New()
		_, err = io.Copy(io.MultiWriter(body, hash), http.MaxBytesReader(w, r.Body, limit))
		_ = rc.SetReadDeadline(time.Time{})
		if err != nil {
			if _, tooLarge := err.(*http.MaxBytesError); tooLarge {
				writeTooLarge(w, limit, "")
			} else {
				writeJSON(w, 400, errorResponse{Error: "invalid request body"})
			}
			return
		}
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			writeJSON(w, 500, errorResponse{Error: "could not read request body"})
			return
		}
		defer r.Body.Close()
		r.Body = body
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		// Concrete path and canonical query keep sites, versions and options
		// distinct; a route pattern alone would collide across sites.
		k := createIdempotencyKey{account, siteID + ":" + r.Method + " " + r.URL.EscapedPath() + "?" + r.URL.Query().Encode(), key}
		for {
			s.mu.Lock()
			old := s.entries[k]
			if old != nil && !old.expires.IsZero() && !s.now().Before(old.expires) {
				s.deleteLocked(k)
				old = nil
			}
			if old != nil {
				s.mu.Unlock()
				if old.hash != digest {
					writeJSON(w, http.StatusConflict, errorResponse{Error: "Idempotency-Key was already used with a different request body", Code: "idempotency_key_reused"})
					return
				}
				select {
				case <-r.Context().Done():
					return
				case <-old.done:
				}
				if old.status == 0 { // The original handler panicked; allow a retry.
					continue
				}
				old.write(w)
				return
			}
			if len(s.entries) >= createIdempotencyMaxTotal || s.accountCounts[account] >= createIdempotencyMaxPerAccount {
				s.mu.Unlock()
				writeJSON(w, http.StatusTooManyRequests, errorResponse{
					Error: "too many pending Idempotency-Key entries; wait for existing keys to expire (24h) or retry without one",
					Code:  "idempotency_cache_full",
				})
				return
			}
			entry := &createIdempotencyResponse{hash: digest, done: make(chan struct{})}
			s.entries[k] = entry
			s.accountCounts[account]++
			s.mu.Unlock()
			// Release waiters even if the handler panics. No partial response is
			// cached in that case; net/http retains its normal panic behavior.
			defer func() {
				s.mu.Lock()
				if entry.status == 0 {
					s.deleteLocked(k)
				}
				close(entry.done)
				s.mu.Unlock()
			}()
			rec := &bufferingResponseWriter{header: make(http.Header), underlying: w}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			s.mu.Lock()
			entry.status, entry.header, entry.body = status, rec.header.Clone(), rec.body.Bytes()
			entry.expires = s.now().Add(createIdempotencyTTL)
			s.mu.Unlock()
			entry.write(w)
			return
		}
	})
}

func (v *createIdempotencyResponse) write(w http.ResponseWriter) {
	for k, values := range v.header {
		w.Header()[k] = append([]string(nil), values...)
	}
	w.WriteHeader(v.status)
	_, _ = w.Write(v.body)
}
