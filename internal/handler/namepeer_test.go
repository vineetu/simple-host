package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNamePeer(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	RegisterNamePeer(mux, []string{" EXAMPLE.TEST "}, func(_ context.Context, zone, name string) (bool, error) {
		calls++
		if zone != "example.test" {
			t.Errorf("zone = %q", zone)
		}
		if name == "failure" {
			return false, errors.New("lookup failed")
		}
		return name == "taken", nil
	})
	for _, tc := range []struct {
		name, remote, header, query string
		status                      int
		body                        string
		calls                       int
	}{
		{"loopback", "127.0.0.1:1234", "", "zone=EXAMPLE.TEST&name=%20TAKEN%20", 200, "{\"taken\":true}\n", 1},
		{"IPv6 loopback", "[::1]:1234", "", "zone=example.test&name=free", 200, "{\"taken\":false}\n", 1},
		{"other zone", "127.0.0.1:1234", "", "zone=other.test&name=taken", 200, "{\"taken\":false}\n", 0},
		{"empty name", "127.0.0.1:1234", "", "zone=example.test", 200, "{\"taken\":false}\n", 0},
		{"lookup failure", "127.0.0.1:1234", "", "zone=example.test&name=failure", 500, "{\"error\":\"internal server error\"}\n", 1},
		{"remote", "192.0.2.1:1234", "", "zone=example.test&name=taken", 404, "404 page not found\n", 0},
		{"forwarded", "127.0.0.1:1234", "X-Forwarded-For", "zone=example.test&name=taken", 404, "404 page not found\n", 0},
		{"real IP", "127.0.0.1:1234", "X-Real-IP", "zone=example.test&name=taken", 404, "404 page not found\n", 0},
		{"missing port", "127.0.0.1", "", "zone=example.test&name=taken", 404, "404 page not found\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/internal/names/taken?"+tc.query, nil)
			r.RemoteAddr = tc.remote
			if tc.header != "" {
				r.Header.Set(tc.header, "192.0.2.1")
			}
			before := calls
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || w.Body.String() != tc.body || calls-before != tc.calls {
				t.Fatalf("response = %d %q; callback calls = %d", w.Code, w.Body.String(), calls-before)
			}
		})
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := NamePeerClient(srv.URL)
	for _, name := range []string{"taken", "free", "failure"} {
		taken, err := client(context.Background(), "example.test", name)
		if taken != (name == "taken") || (err != nil) != (name == "failure") {
			t.Errorf("client(%q) = %v, %v", name, taken, err)
		}
	}

	for _, body := range []string{`{}`, `{"taken":null}`, `not JSON`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			if taken, err := NamePeerClient(srv.URL)(context.Background(), "example.test", "taken"); taken || err == nil {
				t.Fatalf("unreadable answer = %v, %v", taken, err)
			}
		})
	}
}
