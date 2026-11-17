package static

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

func TestHTTPMissingAddressOneBoundedLookup(t *testing.T) {
	o, _ := snapshotFixture(t)
	lookups := 0
	o.Lookup = func(ctx context.Context, _ model.RoutingAddress) (model.SiteTable, error) {
		lookups++
		<-ctx.Done()
		return model.SiteTable{}, ctx.Err()
	}
	copy := New(o)
	h := NewHandler(copy, http.NotFoundHandler(), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if retry(w, r, model.RoutingAddress{Handle: "missing", Name: "page"}) {
				return
			}
			http.NotFound(w, r)
		})
	})
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://page.missing.example/", nil))
	if w.Code != 404 || lookups != 1 || time.Since(start) > time.Second {
		t.Fatalf("miss response %d, lookups %d, elapsed %s", w.Code, lookups, time.Since(start))
	}
}

func TestHTTPNoCopyAndAPIHandover(t *testing.T) {
	o := Options{DataDir: t.TempDir(), StateDir: t.TempDir(), Product: "simple-host", Logf: func(string, ...any) {}, Load: func(context.Context) (model.SiteTable, time.Time, error) {
		return model.SiteTable{}, time.Time{}, errors.New("offline")
	}}
	copy := New(o)
	h := NewHandler(copy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }), func(model.SiteTable, http.Handler, func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		t.Fatal("built pages without a copy")
		return nil
	}, func(r *http.Request) bool { return r.URL.Path == "/signin" })
	for _, tc := range []struct {
		path   string
		status int
	}{{"/", 503}, {"/internal/tls-ask?domain=example.test", 403}, {"/v1/site-signin", 202}, {"/signin", 202}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test"+tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d want %d", tc.path, w.Code, tc.status)
		}
		if tc.status == 503 && w.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
	}
}

func TestHTTPGroupMissAddsRoutingFactsWithoutAWebsite(t *testing.T) {
	o, _ := snapshotFixture(t)
	calls := 0
	o.Lookup = func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		calls++
		return model.SiteTable{Groups: []model.RouteGroup{{ID: "new-group", Slug: "new", AccountID: "new-owner"}}}, nil
	}
	copy := New(o)
	if found, err := copy.Lookup(context.Background(), model.RoutingAddress{Group: "new"}); err != nil || !found {
		t.Fatalf("group miss: %v %v", found, err)
	}
	if calls != 1 || len(copy.Current().Table.Groups) != 1 || copy.Current().Table.Groups[0].Slug != "new" {
		t.Fatal("group-only routing facts were not retained")
	}
}
