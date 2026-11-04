package handler

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/app/legacy"
)

func TestEntryPreparationNoRowsResponse(t *testing.T) {
	for _, route := range []string{"owner", "board", "visitor", ""} {
		t.Run(route, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := &legacy.EntryStageError{Stage: "preparation", Err: sql.ErrNoRows}
			if !new(SiteHandler).writeEntrySelection(w, legacy.EntryRequest{}, legacy.EntryResult{Route: route}, err, "update") {
				t.Fatal("preparation error was not handled")
			}
			if w.Code != http.StatusInternalServerError || w.Body.String() != "{\"error\":\"internal server error\"}\n" {
				t.Fatalf("response = %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestEntryCSRFResponseOrdering(t *testing.T) {
	for _, tc := range []struct{ route, body string }{
		{"owner", "{\"code\":\"csrf_required\",\"error\":\"missing CSRF header\"}\n"},
		{"board", "{\"error\":\"missing CSRF header\",\"code\":\"csrf_required\"}\n"},
	} {
		t.Run(tc.route, func(t *testing.T) {
			for _, operation := range []string{"update", "withdraw"} {
				w := httptest.NewRecorder()
				new(SiteHandler).writeEntrySelection(w, legacy.EntryRequest{}, legacy.EntryResult{Route: tc.route}, &legacy.Error{Code: "csrf_required"}, operation)
				if w.Code != http.StatusForbidden || w.Body.String() != tc.body {
					t.Fatalf("%s response = %d %q", operation, w.Code, w.Body.String())
				}
			}
		})
	}
}

type entryReadCounter struct {
	io.Reader
	bytes int
}

func (r *entryReadCounter) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += n
	return n, err
}

func TestEntryPatchRefusalsDoNotReadBody(t *testing.T) {
	for _, refusal := range []string{"suspended", "csrf", "ownership", "saver"} {
		t.Run(refusal, func(t *testing.T) {
			s := newKindsSite(t, false)
			s.declare(t, "rsvps", map[string]any{"kind": "entries"})
			base := "/v1/sites/shop/data/rsvps"
			id := idOf(t, s.visitor(t, "POST", base, map[string]any{"n": 1}))
			headers := browser(s.dom, s.vicCooky)
			status, code := http.StatusForbidden, "csrf_required"
			switch refusal {
			case "suspended":
				uid, _ := s.a.userID(t, s.vic)
				// Keep the session to exercise the suspension recheck after resolution.
				if _, err := s.a.database.Exec(`UPDATE users SET suspended_at = now(), suspended_reason = 'test' WHERE id = $1`, uid); err != nil {
					t.Fatal(err)
				}
				code = "account_suspended"
			case "csrf":
				delete(headers, "X-SH-CSRF")
			case "ownership":
				headers = browser(s.dom, s.wesCooky)
				status, code = http.StatusNotFound, "not_found"
			case "saver":
				wantCode(t, "block", s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"collection": "rsvps", "id": id}), 200, "")
				code = "not_allowed_to_save"
			}
			body := &entryReadCounter{Reader: strings.NewReader(`{"n":2}`)}
			req := httptest.NewRequest("PATCH", "https://"+s.dom+base+"/items/"+id, body)
			req.Header.Set("X-Forwarded-Proto", "https")
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			s.a.srv.Config.Handler.ServeHTTP(w, req)
			wantCode(t, refusal, resp{w.Code, w.Header(), w.Body.Bytes()}, status, code)
			if body.bytes != 0 {
				t.Fatalf("read %d body bytes before refusing", body.bytes)
			}
		})
	}
}

func TestEntryPatchDeclarationLimitClosesConnection(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "rsvps", map[string]any{"kind": "entries"})
	base := "/v1/sites/shop/data/rsvps"
	id := idOf(t, s.visitor(t, "POST", base, map[string]any{"n": 1}))
	raw := `{"text":"` + strings.Repeat("x", 20<<10) + `"}`
	req, err := http.NewRequest("PATCH", s.a.srv.URL+base+"/items/"+id, strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = s.dom
	req.Header.Set("X-Forwarded-Proto", "https")
	for k, v := range browser(s.dom, s.vicCooky) {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "oversized patch", resp{res.StatusCode, res.Header, data}, http.StatusRequestEntityTooLarge, "item_too_large")
	if !res.Close {
		t.Fatal("oversized patch did not close its HTTP/1 connection")
	}
	// Exercise the same handler with a counted body to check the reader itself
	// stops at the declaration limit rather than consuming the old 64 KB cap.
	counted := &entryReadCounter{Reader: strings.NewReader(raw)}
	req = httptest.NewRequest("PATCH", "https://"+s.dom+base+"/items/"+id, counted)
	for k, v := range browser(s.dom, s.vicCooky) {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.a.srv.Config.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	limit := int(s.a.sites.savedData.EntryMaxKB) << 10
	if counted.bytes != limit+1 {
		t.Fatalf("read %d bytes, want declaration limit + 1 (%d)", counted.bytes, limit+1)
	}
}
