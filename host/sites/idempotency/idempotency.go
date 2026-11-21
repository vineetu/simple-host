// Package idempotency replays the response of a create request (a POST with
// an Idempotency-Key) to a retry of the same request by the same account.
package idempotency

import (
	"crypto/sha256"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
)

const entryTTL = 24 * time.Hour

// Caps bound worst-case memory from an authenticated caller minting many
// distinct keys (including ones that only ever produce an error response,
// which are cached like any other). Hit either and new keys are refused
// before their handler runs; existing keys keep replaying normally.
const (
	maxPerAccount = 500
	maxTotal      = 50000
)

type entryKey struct {
	account, route, key string
}

type response struct {
	hash    [sha256.Size]byte
	done    chan struct{}
	expires time.Time
	status  int
	header  http.Header
	body    []byte
}

// Store keeps response bytes and request hashes in memory, local to this
// process and lost on restart.
type Store struct {
	limits        edition.Limits
	mu            sync.Mutex
	entries       map[entryKey]*response
	accountCounts map[string]int
	now           func() time.Time
}

func New(l edition.Limits) *Store {
	return &Store{
		limits:        l,
		entries:       make(map[entryKey]*response),
		accountCounts: make(map[string]int),
		now:           time.Now,
	}
}

// deleteLocked removes k and keeps accountCounts consistent; callers hold s.mu.
func (s *Store) deleteLocked(k entryKey) {
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

func (s *Store) EvictExpired() int {
	removed := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.entries {
		if !v.expires.IsZero() && !s.now().Before(v.expires) {
			s.deleteLocked(k)
			removed++
		}
	}
	return removed
}

// Wrap belongs inside authentication and rate limiting, outside the response
// notice middleware, so authentication stays live and replayed bytes stay exact.
func (s *Store) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user == nil {
			next.ServeHTTP(w, r)
			return
		}
		s.WrapAccount(user.ID, "", next).ServeHTTP(w, r)
	})
}

// Storage handlers already resolve their caller and resource permissions. They
// supply that identity and resolved site ID without changing their auth path.
func (s *Store) WrapAccount(account, siteID string, next http.Handler) http.Handler {
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
			httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not read request body"})
			return
		}
		defer os.Remove(body.Name())
		defer body.Close()
		limit := max(sites.JSONBodyLimit(sites.LargestSiteLimit(s.limits)), sites.PhotoBodyLimit())
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(httpx.BodyReadTimeout(limit)))
		hash := sha256.New()
		query := r.URL.Query()
		for _, name := range []string{"access", "viewers"} {
			for _, value := range query[name] {
				_, _ = io.WriteString(hash, name+"="+value+"\x00")
			}
			query.Del(name)
		}
		_, err = io.Copy(io.MultiWriter(body, hash), http.MaxBytesReader(w, r.Body, limit))
		_ = rc.SetReadDeadline(time.Time{})
		if err != nil {
			if _, tooLarge := err.(*http.MaxBytesError); tooLarge {
				e := sites.TooLarge(limit, "")
				httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, httpx.ErrorResponse{Error: e.Message, Code: e.Code, Hint: sites.StorageTips})
			} else {
				httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid request body"})
			}
			return
		}
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not read request body"})
			return
		}
		defer r.Body.Close()
		r.Body = body
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		// Concrete path and canonical query keep sites, versions and options
		// distinct; a route pattern alone would collide across sites. Creation
		// access/viewers are hashed with the body so changing them refuses reuse.
		k := entryKey{account, siteID + ":" + r.Method + " " + r.URL.EscapedPath() + "?" + query.Encode(), key}
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
					httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "Idempotency-Key was already used with a different request body or access/viewers", Code: "idempotency_key_reused"})
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
			if len(s.entries) >= maxTotal || s.accountCounts[account] >= maxPerAccount {
				s.mu.Unlock()
				httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{
					Error: "too many pending Idempotency-Key entries; wait for existing keys to expire (24h) or retry without one",
					Code:  "idempotency_cache_full",
				})
				return
			}
			entry := &response{hash: digest, done: make(chan struct{})}
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
			rec := httpx.NewBufferedResponse(w)
			next.ServeHTTP(rec, r)
			status := rec.Status()
			if status == 0 {
				status = http.StatusOK
			}
			s.mu.Lock()
			entry.status, entry.header, entry.body = status, rec.Header().Clone(), rec.Body()
			entry.expires = s.now().Add(entryTTL)
			s.mu.Unlock()
			entry.write(w)
			return
		}
	})
}

func (v *response) write(w http.ResponseWriter) {
	for k, values := range v.header {
		w.Header()[k] = append([]string(nil), values...)
	}
	w.WriteHeader(v.status)
	_, _ = w.Write(v.body)
}
