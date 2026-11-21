package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/idempotency"
)

// A replayed create keeps the bytes NoticeMiddleware wrote the first time.
func TestCreateIdempotencyReplayAndConflict(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := idempotency.New(config.DefaultLimits())
			calls := 0
			h := store.Wrap(NoticeMiddleware("9.0.0")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func createRequest(account, method, path, key, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if account != "" {
		r = r.WithContext(auth.WithUser(r.Context(), &model.User{ID: account}))
	}
	r.Header.Set("Idempotency-Key", key)
	return r
}
