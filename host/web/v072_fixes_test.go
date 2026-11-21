package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Small-box trial B6: a response has the same shape whether or not the
// caller sends X-Skill-Version: arrays stay bare (the notice is a header),
// and an object keeps its fields byte for byte, big numbers included.
func TestNoticeKeepsResponseShape(t *testing.T) {
	mw := NoticeMiddleware("1.2.3")
	serve := func(body string, hdr string) *httptest.ResponseRecorder {
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		req := httptest.NewRequest("GET", "/v1/sites", nil)
		if hdr != "" {
			req.Header.Set("X-Skill-Version", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	arr := `[{"id":1},{"id":2}]`
	for _, v := range []string{"", "0.1.0", "1.2.3"} {
		rec := serve(arr, v)
		if rec.Body.String() != arr {
			t.Fatalf("skill %q: array became %s", v, rec.Body.String())
		}
		if stale := v == "0.1.0"; (rec.Header().Get("X-Skill-Notice") != "") != stale {
			t.Fatalf("skill %q: notice header %q", v, rec.Header().Get("X-Skill-Notice"))
		}
	}
	obj := `{"n":12345678901234567890,"f":1.10,"s":"x"}`
	rec := serve(obj, "0.1.0")
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("object: %v %s", err, rec.Body.String())
	}
	if string(got["n"]) != "12345678901234567890" || string(got["f"]) != "1.10" || got["_notice"] == nil {
		t.Fatalf("object changed: %s", rec.Body.String())
	}
	if rec := serve(`{}`, "0.1.0"); !strings.HasPrefix(rec.Body.String(), `{"_notice":"`) || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("empty object: %s", rec.Body.String())
	}
	for _, v := range []string{"1.2.3", ""} {
		if rec := serve(obj, v); rec.Body.String() != obj || rec.Header().Get("X-Skill-Notice") != "" {
			t.Fatalf("skill %q (current, or none: a plain API call): %s", v, rec.Body.String())
		}
	}
}
