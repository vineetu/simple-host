package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSidecar is an OpenAI-compatible endpoint that records what it was sent.
type fakeSidecar struct {
	mu      sync.Mutex
	headers []http.Header
	bodies  []string
	answer  string
	status  int
}

func (f *fakeSidecar) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.bodies = append(f.bodies, string(b))
		f.mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": f.answer}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestAsk(t *testing.T, f *fakeSidecar, burst, daily int) (*AskHandler, *http.ServeMux) {
	srv := f.server(t)
	h := NewAskHandler("sidecar-key", srv.URL+"/v1", "grok-test", burst, 20*time.Second, daily)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", h.ask)
	return h, mux
}

func postAsk(mux *http.ServeMux, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/ask", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.7:4321"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func askCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e errorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	return e.Code
}

func TestAskValidation(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 100, 100)
	cases := []struct {
		name, body string
		code       string
	}{
		{"bad json", `{`, "invalid_body"},
		{"unknown page", `{"question":"hi","page":"index"}`, "unknown_page"},
		{"missing page", `{"question":"hi"}`, "unknown_page"},
		{"empty", `{"question":"   ","page":"architecture"}`, "empty_question"},
		{"too long", `{"question":"` + strings.Repeat("é", 501) + `","page":"architecture"}`, "question_too_long"},
	}
	for _, c := range cases {
		w := postAsk(mux, c.body, nil)
		if w.Code != http.StatusBadRequest || askCode(t, w) != c.code {
			t.Errorf("%s: got %d %s, want 400 %s", c.name, w.Code, w.Body.String(), c.code)
		}
	}
	if len(f.bodies) != 0 {
		t.Fatalf("invalid requests reached the model: %d", len(f.bodies))
	}
	w := postAsk(mux, `{"question":"`+strings.Repeat("é", 500)+`","page":"architecture"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("500-char question: got %d %s", w.Code, w.Body.String())
	}
}

func TestAskRateLimitPerIP(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 2, 100)
	body := `{"question":"what is it?","page":"architecture"}`
	for i := 0; i < 2; i++ {
		if w := postAsk(mux, body, nil); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := postAsk(mux, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: got %d, want 429", w.Code)
	}
	// Another visitor has their own bucket.
	if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1" }); w.Code != http.StatusOK {
		t.Fatalf("other IP: got %d", w.Code)
	}
}

func TestAskDailyCap(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	h, mux := newTestAsk(t, f, 100, 2)
	day := time.Date(2026, 9, 27, 23, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return day }
	body := `{"question":"what is it?","page":"features"}`
	for i := 0; i < 2; i++ {
		if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.2." + string(rune('1'+i)) + ":1" }); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.2.9:1" })
	if w.Code != http.StatusTooManyRequests || askCode(t, w) != "daily_limit" {
		t.Fatalf("over the cap: got %d %s", w.Code, w.Body.String())
	}
	if len(f.bodies) != 2 {
		t.Fatalf("model called %d times, want 2", len(f.bodies))
	}
	day = day.Add(2 * time.Hour) // next UTC day
	if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.2.9:1" }); w.Code != http.StatusOK {
		t.Fatalf("next day: got %d", w.Code)
	}
}

func TestAskPageRouting(t *testing.T) {
	f := &fakeSidecar{answer: "An answer."}
	_, mux := newTestAsk(t, f, 100, 100)
	want := map[string][]string{
		"architecture":            {"About Simple Host (public summary)", "Every place", "support@simple-host.app"},
		"features":                {"About Simple Host (public summary)", "Everything it does."},
		"enterprise-brief":        {"About Simple Host Enterprise", "Stateful static websites, on your own infrastructure.", "Give everyone in the company a place to build.", "I don't know from these pages."},
		"enterprise-architecture": {"About Simple Host Enterprise", "How Simple Host Enterprise is built", "I don't know from these pages."},
	}
	for page, needles := range want {
		f.bodies = nil
		w := postAsk(mux, `{"question":"how does sign-in work?","page":"`+page+`"}`, func(r *http.Request) { r.RemoteAddr = "192.0.2.50:1" })
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", page, w.Code, w.Body.String())
		}
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["answer"] != "An answer." {
			t.Errorf("%s: answer %q", page, resp["answer"])
		}
		var sent openAIRequest
		if err := json.Unmarshal([]byte(f.bodies[0]), &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Model != "grok-test" || len(sent.Messages) != 2 || sent.Messages[1].Content != "how does sign-in work?" {
			t.Fatalf("%s: unexpected request %+v", page, sent.Messages[1:])
		}
		sys := sent.Messages[0].Content
		for _, n := range needles {
			if !strings.Contains(sys, n) {
				t.Errorf("%s: system prompt lacks %q", page, n)
			}
		}
		ent := strings.HasPrefix(page, "enterprise")
		if ent && strings.Contains(sys, "support@simple-host.app") {
			t.Errorf("%s: enterprise answers must not carry contact details", page)
		}
		if !ent && strings.Contains(sys, "About Simple Host Enterprise") {
			t.Errorf("%s: hosted page got the enterprise pack", page)
		}
	}
}

// Nothing identifying the visitor may reach the model: not their address,
// browser, cookies or referring page.
func TestAskSendsNoVisitorDetails(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 100, 100)
	w := postAsk(mux, `{"question":"what is it?","page":"enterprise-brief"}`, func(r *http.Request) {
		r.RemoteAddr = "198.51.100.77:5555"
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		r.Header.Set("X-Real-IP", "203.0.113.99")
		r.Header.Set("User-Agent", "VisitorBrowser/9.9")
		r.Header.Set("Cookie", "sh_session=visitor-cookie-value")
		r.Header.Set("Referer", "https://simple-host.app/enterprise/brief?from=visitor-ref")
		r.Header.Set("Accept-Language", "xx-VISITOR")
	})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("model calls: %d", len(f.bodies))
	}
	var dump strings.Builder
	dump.WriteString(f.bodies[0])
	for k, vs := range f.headers[0] {
		dump.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Content-Type", "Content-Length", "Accept-Encoding", "User-Agent":
		default:
			t.Errorf("unexpected header to the model: %s", k)
		}
	}
	for _, leak := range []string{"198.51.100.77", "203.0.113.99", "VisitorBrowser", "visitor-cookie-value", "visitor-ref", "xx-VISITOR"} {
		if strings.Contains(dump.String(), leak) {
			t.Errorf("visitor detail %q reached the model", leak)
		}
	}
	var sent map[string]any
	json.Unmarshal([]byte(f.bodies[0]), &sent)
	for k := range sent {
		if k != "model" && k != "messages" && k != "max_tokens" && k != "temperature" {
			t.Errorf("unexpected field in the model request: %s", k)
		}
	}
}

func TestAskModelFailure(t *testing.T) {
	f := &fakeSidecar{status: http.StatusTooManyRequests}
	_, mux := newTestAsk(t, f, 100, 100)
	w := postAsk(mux, `{"question":"what is it?","page":"architecture"}`, nil)
	if w.Code != http.StatusBadGateway || askCode(t, w) != "unavailable" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

// The knowledge packs are built from public pages, but lines naming machine
// details or the operator's personal details never reach the model.
func TestAskPacksHoldNoInternalDetails(t *testing.T) {
	forbidden := []string{"/etc/", "/opt/", "/srv/", "/var/", "/usr/local", "/home/", "/root/", "127.0.0.1", "localhost", ":8102", "vineetu@gmail", "@gmail.com", "cliproxy", "CLIProxy", "simple-host.env", "journalctl", "INTENT.md", "PARITY.md", "Vineet"}
	for page := range askPages {
		pack := askPack(page)
		if len(pack) < 2000 {
			t.Errorf("%s: pack suspiciously small (%d bytes)", page, len(pack))
		}
		for _, s := range forbidden {
			if strings.Contains(pack, s) {
				t.Errorf("%s: pack contains %q", page, s)
			}
		}
		if strings.Contains(pack, "<script") || strings.Contains(pack, "{ max-width") {
			t.Errorf("%s: pack contains markup or CSS", page)
		}
	}
}

func TestAskCleanAnswer(t *testing.T) {
	in := "## Heading\nSee **the** [features](https://simple-host.app/features) and [evil](https://evil.example/x) or [js](javascript:void)."
	got := cleanAnswer(in)
	want := "Heading\nSee the [features](https://simple-host.app/features) and evil or js."
	if got != want {
		t.Fatalf("cleanAnswer:\n got %q\nwant %q", got, want)
	}
	long := strings.Repeat("word ", 250)
	if n := len(strings.Fields(cleanAnswer(long))); n != askMaxAnswerWords {
		t.Fatalf("long answer kept %d words", n)
	}
}

// The box renders only when the server has it on, and only on its pages.
func TestAskWidgetOnlyWhenEnabled(t *testing.T) {
	prev := askEnabled
	t.Cleanup(func() { askEnabled = prev })
	pages := map[string]string{
		"/architecture.html":       "architecture.html",
		"/features":                "features.html",
		"/enterprise/brief":        "enterprise-brief.html",
		"/enterprise/architecture": "enterprise-architecture.html",
	}
	for _, on := range []bool{false, true} {
		askEnabled = on
		for path, file := range pages {
			body, err := chromePage(file, chromeDataFor(httptest.NewRequest("GET", path, nil), ""))
			if err != nil {
				t.Fatal(err)
			}
			s := string(body)
			if strings.Contains(s, "<!--sh:ask-->") {
				t.Errorf("%s: marker left in the page", path)
			}
			has := strings.Contains(s, `class="sh-ask"`) && strings.Contains(s, "/ask.js?v=")
			if has != on {
				t.Errorf("%s: widget present=%v with the box on=%v", path, has, on)
			}
			if on && !strings.Contains(s, `data-page="`+askPageByPath[path]+`"`) {
				t.Errorf("%s: wrong page key", path)
			}
		}
		// A page without a pack never gets the box.
		body, _ := chromePage("enterprise.html", chromeDataFor(httptest.NewRequest("GET", "/enterprise", nil), ""))
		if strings.Contains(string(body), `class="sh-ask"`) {
			t.Errorf("/enterprise got the box")
		}
	}
}
