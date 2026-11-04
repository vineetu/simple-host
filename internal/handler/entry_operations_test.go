package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
