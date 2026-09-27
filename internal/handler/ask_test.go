package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
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

const testApex = "https://simple-host.app"

func newTestAsk(t *testing.T, f *fakeSidecar, burst, daily int) (*AskHandler, *http.ServeMux) {
	return newTestAskOpts(t, f, AskOptions{Burst: burst, Every: 20 * time.Second, DailyMax: daily, MaxInFlight: 4})
}

func newTestAskOpts(t *testing.T, f *fakeSidecar, o AskOptions) (*AskHandler, *http.ServeMux) {
	srv := f.server(t)
	h := newAskHandler("sidecar-key", srv.URL+"/v1", "grok-test", testApex+"/", &askMemCounter{}, o)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", h.ask)
	return h, mux
}

func postAsk(mux http.Handler, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/ask", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", testApex)
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
		{"unknown page (older form)", `{"question":"hi","page":"index"}`, "unknown_assistant"},
		{"no assistant or page", `{"question":"hi"}`, "unknown_assistant"},
		{"unknown assistant", `{"question":"hi","assistant":"docs","page":"features"}`, "unknown_assistant"},
		{"page of the other assistant", `{"question":"hi","assistant":"enterprise","page":"features"}`, "unknown_page"},
		{"unknown page", `{"question":"hi","assistant":"simple-host","page":"index"}`, "unknown_page"},
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

// Each assistant answers from its one combined pack, whichever of its pages the
// reader is on; the request names that page so answers can prefer it. The
// older {page} form picks the page's assistant and gets the same prompt.
func TestAskAssistantRouting(t *testing.T) {
	f := &fakeSidecar{answer: "An answer."}
	_, mux := newTestAsk(t, f, 100, 100)
	hosted := []string{"About Simple Host (public summary)", "Everything it does.", "How Simple Host is built (public summary", "support@simple-host.app",
		"=== Text of the page https://simple-host.app/features ===", "=== Text of the page https://simple-host.app/architecture.html ==="}
	ent := []string{"About Simple Host Enterprise", "Give everyone in the company a place to build", "Stateful static websites, on your own infrastructure.",
		"How Simple Host Enterprise is built", "I don't know from these pages.", "You are the Simple Host Enterprise assistant.",
		"=== Text of the page https://simple-host.app/enterprise ===", "=== Text of the page https://simple-host.app/enterprise/brief ===",
		"=== Text of the page https://simple-host.app/enterprise/architecture ==="}
	cases := []struct {
		body      string
		assistant string
		on        string // the page the prompt says the reader is on, "" for none
		needles   []string
	}{
		{`"assistant":"simple-host","page":"features"`, "simple-host", "/features", hosted},
		{`"assistant":"simple-host","page":"architecture"`, "simple-host", "/architecture.html", hosted},
		{`"assistant":"simple-host"`, "simple-host", "", hosted},
		{`"assistant":"enterprise","page":"enterprise"`, "enterprise", "/enterprise", ent},
		{`"assistant":"enterprise","page":"enterprise-brief"`, "enterprise", "/enterprise/brief", ent},
		{`"assistant":"enterprise","page":"enterprise-architecture"`, "enterprise", "/enterprise/architecture", ent},
		// The older form, kept for one release.
		{`"page":"features"`, "simple-host", "/features", hosted},
		{`"page":"architecture"`, "simple-host", "/architecture.html", hosted},
		{`"page":"enterprise-brief"`, "enterprise", "/enterprise/brief", ent},
		{`"page":"enterprise-architecture"`, "enterprise", "/enterprise/architecture", ent},
		{`"page":"enterprise"`, "enterprise", "/enterprise", ent},
	}
	for _, c := range cases {
		f.bodies = nil
		w := postAsk(mux, `{"question":"how does sign-in work?",`+c.body+`}`, func(r *http.Request) { r.RemoteAddr = "192.0.2.50:1" })
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.body, w.Code, w.Body.String())
		}
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["answer"] != "An answer." {
			t.Errorf("%s: answer %q", c.body, resp["answer"])
		}
		var sent openAIRequest
		if err := json.Unmarshal([]byte(f.bodies[0]), &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Model != "grok-test" || len(sent.Messages) != 2 || sent.Messages[1].Content != "how does sign-in work?" {
			t.Fatalf("%s: unexpected request %+v", c.body, sent.Messages[1:])
		}
		sys := sent.Messages[0].Content
		a := askAssistantByKey(c.assistant)
		if want := askSystemPrompt(a, pageKeyIn(c.body)); sys != want {
			t.Errorf("%s: prompt differs from the %s assistant's", c.body, c.assistant)
		}
		for _, n := range c.needles {
			if !strings.Contains(sys, n) {
				t.Errorf("%s: system prompt lacks %q", c.body, n)
			}
		}
		onLine := "The reader is on https://simple-host.app" + c.on + "."
		if c.on == "" {
			if strings.Contains(sys, "The reader is on") {
				t.Errorf("%s: prompt names a page the reader is not known to be on", c.body)
			}
		} else if !strings.Contains(sys, onLine) {
			t.Errorf("%s: prompt lacks %q", c.body, onLine)
		}
		if c.assistant == "enterprise" && strings.Contains(sys, "support@simple-host.app") {
			t.Errorf("%s: enterprise answers must not carry contact details", c.body)
		}
		if c.assistant == "simple-host" && strings.Contains(sys, "About Simple Host Enterprise") {
			t.Errorf("%s: the Simple Host assistant got the enterprise pack", c.body)
		}
	}
}

// pageKeyIn is the "page" value of a test body fragment, "" when absent.
func pageKeyIn(body string) string {
	_, after, ok := strings.Cut(body, `"page":"`)
	if !ok {
		return ""
	}
	key, _, _ := strings.Cut(after, `"`)
	return key
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
		switch k {
		case "model", "messages", "max_tokens", "temperature", "stream", "reasoning_effort":
		default:
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

// askForbiddenSamples are strings the line filter must catch. The pack test
// checks the same filter (askLineForbidden) over every line the model gets,
// so the list and the regex cannot drift apart.
var askForbiddenSamples = []string{
	"/etc/simple-host.env", "reads /opt/x", "(/srv/simple-host/sites)", "/var/log/a", "/usr/local/bin", "/home/ubuntu", "/root/.x", "/mnt/disk", "/data/x", "/lib/x",
	"127.0.0.1", "localhost", "10.0.0.8", "listens on ::1", "[::1]:80", "fe80::1", "2001:db8:0:1::5", "2001:db8:85a3:0:0:8a2e:370:7334",
	"on :8090", "port 8102", "deploy/domain-certs/issue.sh", "scripts/check.sh", "internal/handler/ask.go", "./cmd/server", "~/.config", "GET /internal/showcase/x", "db.internal", "box.local",
	"the ADMIN_API_KEY", "LLM_API_KEY", "ANALYTICS_SALT_SECRET", "RESEND_API_KEYS", "TRANSCRIBE_TICKET_TOKEN",
	"call (415) 555-0132", "+44 20 7946 0958", "+1 510 555 3956",
	"someone@example.com", "vineetu@gmail.com", "Vineet", "Sriram",
	"systemd unit", "journalctl -u x", "sudo cp", "cliproxy", "the sidecar", "over loopback", "the simplehost user", "INTENT.md", "PARITY.md", "burst 20",
}

func TestAskFilterCatchesSamples(t *testing.T) {
	for _, s := range askForbiddenSamples {
		if !askLineForbidden(s) {
			t.Errorf("filter lets through %q", s)
		}
	}
	// Honest public lines stay.
	for _, s := range []string{
		"Contact: support@simple-host.app.",
		"There are no local accounts and no admin key.",
		"There is no master key anywhere in the product.",
		"Uploads are data only; .env files and server scripts such as .php are refused.",
		"Sign in by 10:30 to keep your place.",
		"Simple Host is one Go program, one Postgres database and one folder of files.",
		"Every site lives at https://<site>.<handle>.simple-host.app/.",
	} {
		if askLineForbidden(s) {
			t.Errorf("filter drops the public line %q", s)
		}
	}
}

// The knowledge packs are built from public pages and askdata, but lines
// naming machine details or the operator's personal details never reach the
// model: every line of every system prompt passes the filter.
func TestAskPacksHoldNoInternalDetails(t *testing.T) {
	for _, a := range askAssistants {
		pack := askPack(a.key)
		if len(pack) < 2000 {
			t.Errorf("%s: pack suspiciously small (%d bytes)", a.key, len(pack))
		}
		for _, page := range append([]string{""}, askPageKeys(a)...) {
			for _, line := range strings.Split(askSystemPrompt(a, page), "\n") {
				if askLineForbidden(line) {
					t.Errorf("%s/%s: a forbidden line reaches the model: %q", a.key, page, line)
				}
			}
		}
		if strings.Contains(pack, "<script") || strings.Contains(pack, "{ max-width") {
			t.Errorf("%s: pack contains markup or CSS", a.key)
		}
		// One combined pack: every page of the assistant, each once.
		for _, p := range a.pages {
			head := "=== Text of the page https://simple-host.app" + p.paths[0] + " ==="
			if n := strings.Count(pack, head); n != 1 {
				t.Errorf("%s: %s appears %d times in the pack", a.key, p.paths[0], n)
			}
		}
	}
	// The architecture page answers from the curated summary, not the page.
	arch := askPackSection(askPack("simple-host"), "/architecture.html")
	if !strings.Contains(arch, "How Simple Host is built (public summary") {
		t.Errorf("architecture section lacks the curated summary")
	}
	for _, s := range []string{"blast-radius", "archive_sha256", "WRITE_AUTH_MODE", "Feature map", "api_ip_daily"} {
		if strings.Contains(askPack("simple-host"), s) {
			t.Errorf("simple-host pack carries architecture page detail %q", s)
		}
	}
	if n := len(strings.Fields(arch)); n > 2000 {
		t.Errorf("architecture section is %d words; it should be the short summary", n)
	}
}

// Each assistant's whole system prompt stays a reasonable size (about four
// characters a token): the enterprise one within about 12k tokens, the Simple
// Host one (the long features page plus the curated architecture summary)
// within about 14k.
func TestAskPromptSizes(t *testing.T) {
	for key, maxTokens := range map[string]int{"enterprise": 12000, "simple-host": 14000} {
		a := askAssistantByKey(key)
		n := len(askSystemPrompt(a, a.pages[0].key)) / 4
		t.Logf("%s: about %d tokens", key, n)
		if n > maxTokens {
			t.Errorf("%s prompt is about %d tokens, over %d", key, n, maxTokens)
		}
	}
}

func askPageKeys(a *askAssistant) []string {
	var out []string
	for _, p := range a.pages {
		out = append(out, p.key)
	}
	return out
}

// askPackSection is the text under one page's heading in a pack.
func askPackSection(pack, path string) string {
	_, after, ok := strings.Cut(pack, "=== Text of the page https://simple-host.app"+path+" ===")
	if !ok {
		return ""
	}
	sec, _, _ := strings.Cut(after, "\n=== Text of the page ")
	return sec
}

func TestAskCleanAnswer(t *testing.T) {
	hosted, ent := askAssistantByKey("simple-host").links, askAssistantByKey("enterprise").links
	in := "## Heading\nSee **the** [features](https://simple-host.app/features) and [evil](https://evil.example/x) or [js](javascript:void)."
	got := cleanAnswer(in, hosted)
	want := "Heading\nSee the [features](https://simple-host.app/features) and evil or js."
	if got != want {
		t.Fatalf("cleanAnswer:\n got %q\nwant %q", got, want)
	}
	long := strings.Repeat("word ", 250)
	if n := len(strings.Fields(cleanAnswer(long, hosted))); n != askMaxAnswerWords {
		t.Fatalf("long answer kept %d words", n)
	}
	cases := map[string]string{
		// A rejected link whose label is itself an address loses the label.
		"Log in at [https://evil.example/login](https://evil.example).": "Log in at .",
		"Try [www.evil.example](https://evil.example) now.":             "Try now.",
		"Go to [evil.example/login](https://evil.example/login) now.":   "Go to now.",
		// Bare addresses elsewhere go, unless they are a known page.
		"See https://evil.example/x or https://simple-host.app/terms.": "See or https://simple-host.app/terms.",
		// Only known public pages are linked.
		"[docs](https://simple-host.app/docs.html) [me](https://simple-host.app/vineetu) [api](https://simple-host.app/v1/sites)": "[docs](https://simple-host.app/docs.html) me api",
		"[home](https://simple-host.app) [ent](https://simple-host.app/enterprise#who-sees)":                                      "[home](https://simple-host.app) [ent](https://simple-host.app/enterprise#who-sees)",
		// Identifiers with underscores survive; __bold__ does not.
		"The cookie is __Host-sh_vsess and __this__ is bold.": "The cookie is __Host-sh_vsess and this is bold.",
	}
	for in, want := range cases {
		if got := cleanAnswer(in, hosted); got != want {
			t.Errorf("cleanAnswer(%q):\n got %q\nwant %q", in, got, want)
		}
	}
	// Each assistant links only to its own list.
	entCases := map[string]string{
		"[brief](https://simple-host.app/enterprise/brief#costs) and [how](https://simple-host.app/enterprise/architecture)": "[brief](https://simple-host.app/enterprise/brief#costs) and [how](https://simple-host.app/enterprise/architecture)",
		"[details](https://simple-host.app/enterprise) not [docs](https://simple-host.app/docs.html)":                        "[details](https://simple-host.app/enterprise) not docs",
		"[support](https://simple-host.app/support)":                                                                         "support",
	}
	for in, want := range entCases {
		if got := cleanAnswer(in, ent); got != want {
			t.Errorf("enterprise cleanAnswer(%q):\n got %q\nwant %q", in, got, want)
		}
	}
	if got := cleanAnswer("[brief](https://simple-host.app/enterprise/brief)", hosted); got != "brief" {
		t.Errorf("the Simple Host assistant linked an enterprise page: %q", got)
	}
	for _, a := range askAssistants {
		for p := range a.links {
			if !askLinkAllowed("https://simple-host.app"+p, a.links) {
				t.Errorf("%s: known page %s not linkable", a.key, p)
			}
		}
		// Every page of an assistant is on its own link list.
		for _, p := range a.pages {
			if !a.links[p.paths[0]] {
				t.Errorf("%s: its page %s is not linkable", a.key, p.paths[0])
			}
		}
	}
}

// One widget, named by the page: every assistant page (and only those) gets
// the same panel for its assistant when the assistants are on, with the
// assistant's title, the page key, one ask.js; nothing when off.
func TestAskWidgetOnlyWhenEnabled(t *testing.T) {
	prev := askEnabled
	t.Cleanup(func() { askEnabled = prev })
	pages := map[string]struct{ file, assistant, page, title string }{
		"/architecture.html":       {"architecture.html", "simple-host", "architecture", "Ask about Simple Host<"},
		"/features":                {"features.html", "simple-host", "features", "Ask about Simple Host<"},
		"/features.html":           {"features.html", "simple-host", "features", "Ask about Simple Host<"},
		"/enterprise":              {"enterprise.html", "enterprise", "enterprise", "Ask about Simple Host Enterprise<"},
		"/enterprise/brief":        {"enterprise-brief.html", "enterprise", "enterprise-brief", "Ask about Simple Host Enterprise<"},
		"/enterprise/architecture": {"enterprise-architecture.html", "enterprise", "enterprise-architecture", "Ask about Simple Host Enterprise<"},
	}
	for _, on := range []bool{false, true} {
		askEnabled = on
		for path, p := range pages {
			body, err := chromePage(p.file, chromeDataFor(httptest.NewRequest("GET", path, nil), ""))
			if err != nil {
				t.Fatal(err)
			}
			s := string(body)
			if strings.Contains(s, "<!--sh:ask") {
				t.Errorf("%s: marker left in the page", path)
			}
			has := strings.Contains(s, `class="sh-ask"`)
			if has != on {
				t.Errorf("%s: widget present=%v with the assistants on=%v", path, has, on)
			}
			if !on {
				continue
			}
			for _, want := range []string{`data-assistant="` + p.assistant + `"`, `data-page="` + p.page + `"`, p.title} {
				if !strings.Contains(s, want) {
					t.Errorf("%s: lacks %q", path, want)
				}
			}
			if n := strings.Count(s, "/ask.js?v="); n != 1 {
				t.Errorf("%s: ask.js included %d times", path, n)
			}
			if tone := strings.Contains(s, `data-tone="navy"`); tone != (p.assistant == "enterprise") {
				t.Errorf("%s: navy tone=%v", path, tone)
			}
		}
	}
	// No other page carries the widget or a marker.
	askEnabled = true
	for path, file := range map[string]string{"/": "index.html", "/dashboard": "index.html", "/docs.html": "docs.html", "/install.html": "install.html",
		"/privacy.html": "privacy.html", "/hackathons": "hackathons.html", "/support": "support.html", "/terms": "terms.html"} {
		body, err := chromePage(file, chromeDataFor(httptest.NewRequest("GET", path, nil), ""))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), `class="sh-ask"`) || strings.Contains(string(body), "<!--sh:ask") {
			t.Errorf("%s got the widget", path)
		}
	}
}

// Every page of an assistant opts in with one marker naming that assistant;
// no other embedded page carries one.
func TestAskMarkersNameTheirAssistant(t *testing.T) {
	want := map[string]string{}
	for _, a := range askAssistants {
		for _, p := range a.pages {
			want[p.file] = a.key
		}
	}
	entries, err := staticFiles.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		b, _ := staticFiles.ReadFile("static/" + e.Name())
		ms := markerAsk.FindAllSubmatch(b, -1)
		if want[e.Name()] == "" {
			if len(ms) != 0 {
				t.Errorf("%s carries an ask marker but is no assistant's page", e.Name())
			}
			continue
		}
		if len(ms) != 1 || string(ms[0][1]) != want[e.Name()] {
			t.Errorf("%s: want exactly one <!--sh:ask %s-->, got %d", e.Name(), want[e.Name()], len(ms))
		}
	}
}

// Only the apex's own pages may ask: any other Origin, or none, is refused
// before the model or the daily count is touched.
func TestAskSameOriginOnly(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	h, mux := newTestAsk(t, f, 100, 100)
	body := `{"question":"what is it?","page":"features"}`
	for _, o := range []string{"", "https://evil.example", "https://madurai-idly.vineetu.simple-host.app", "https://vineetu.simple-host.app", "http://simple-host.app", "https://simple-host.app.evil.com", "null"} {
		w := postAsk(mux, body, func(r *http.Request) {
			if o == "" {
				r.Header.Del("Origin")
			} else {
				r.Header.Set("Origin", o)
			}
		})
		if w.Code != http.StatusForbidden || askCode(t, w) != "forbidden_origin" {
			t.Errorf("Origin %q: got %d %s, want 403", o, w.Code, w.Body.String())
		}
	}
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		w := postAsk(mux, body, func(r *http.Request) { r.Header.Set("Content-Type", ct) })
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: got %d, want 415", ct, w.Code)
		}
	}
	if len(f.bodies) != 0 {
		t.Fatalf("refused requests reached the model: %d", len(f.bodies))
	}
	if n := h.daily.(*askMemCounter).count; n != 0 {
		t.Fatalf("refused requests used %d daily slots", n)
	}
	if w := postAsk(mux, body, func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }); w.Code != http.StatusOK {
		t.Fatalf("JSON with charset: got %d", w.Code)
	}
	// Another instance accepts its own apex.
	h2 := newAskHandler("k", "http://x/v1", "m", "https://hosting.example.org", &askMemCounter{}, AskOptions{Burst: 1, Every: time.Second, DailyMax: 1, MaxInFlight: 1})
	if h2.origin != "https://hosting.example.org" {
		t.Fatalf("instance apex origin %q", h2.origin)
	}
}

// The global CORS policy leaves /v1/ask alone: no grant, no preflight answer.
func TestAskHasNoCORSGrant(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 100, 100)
	app := CORS(mux)
	w := postAsk(app, `{"question":"q","page":"features"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin ask: %d", w.Code)
	}
	if v := w.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("POST /v1/ask carries Access-Control-Allow-Origin %q", v)
	}
	r := httptest.NewRequest(http.MethodOptions, "/v1/ask", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	pw := httptest.NewRecorder()
	app.ServeHTTP(pw, r)
	if pw.Header().Get("Access-Control-Allow-Origin") != "" || pw.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("preflight for /v1/ask was granted: %d %v", pw.Code, pw.Header())
	}
}

// No shortened IP (or anything else) is kept for /v1/ask in the API metrics.
func TestAskLeftOutOfAPIMetrics(t *testing.T) {
	m := &APIMetrics{routes: map[routeKey]int64{}, ips: map[string]*ipAgg{}}
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 100, 100)
	if w := postAsk(m.Wrap(mux), `{"question":"q","page":"features"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("ask: %d", w.Code)
	}
	if len(m.routes) != 0 || len(m.ips) != 0 {
		t.Fatalf("ask was counted: routes=%v ips=%d", m.routes, len(m.ips))
	}
	// Other API calls still are.
	m.Wrap(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/me", nil))
	if len(m.ips) != 1 {
		t.Fatalf("other /v1 calls are no longer counted")
	}
}

// With every slot busy, a question gets 503 "busy" and no daily slot.
func TestAskInFlightCap(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}}}})
	}))
	t.Cleanup(srv.Close)
	counter := &askMemCounter{}
	h := newAskHandler("k", srv.URL+"/v1", "m", testApex, counter, AskOptions{Burst: 100, Every: time.Second, DailyMax: 100, MaxInFlight: 2})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", h.ask)
	body := `{"question":"q","page":"features"}`
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = fmt.Sprintf("192.0.%d.1:1", i) })
		}(i)
	}
	<-entered
	<-entered
	w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.9.1:1" })
	if w.Code != http.StatusServiceUnavailable || askCode(t, w) != "busy" {
		t.Fatalf("third at once: got %d %s, want 503 busy", w.Code, w.Body.String())
	}
	close(release)
	wg.Wait()
	if counter.count != 2 {
		t.Fatalf("daily count %d, want 2 (busy must not use a slot)", counter.count)
	}
	if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "192.0.9.1:1" }); w.Code != http.StatusOK {
		t.Fatalf("after the others finished: %d", w.Code)
	}
}

// One network (/24) gets askNetShare visitors' worth, then waits.
func TestAskPerNetworkLimit(t *testing.T) {
	f := &fakeSidecar{answer: "ok"}
	_, mux := newTestAsk(t, f, 1, 1000)
	body := `{"question":"q","page":"features"}`
	for i := 1; i <= askNetShare; i++ {
		if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = fmt.Sprintf("203.0.113.%d:1", i) }); w.Code != http.StatusOK {
			t.Fatalf("address %d in the /24: %d", i, w.Code)
		}
	}
	if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "203.0.113.200:1" }); w.Code != http.StatusTooManyRequests {
		t.Fatalf("one more in the same /24: got %d, want 429", w.Code)
	}
	if w := postAsk(mux, body, func(r *http.Request) { r.RemoteAddr = "198.51.100.1:1" }); w.Code != http.StatusOK {
		t.Fatalf("another network: %d", w.Code)
	}
}

// A failed call is one upstream request, and nothing the upstream sent back
// (which might echo the question) reaches the log.
func TestAskUpstreamErrorsLogStatusOnly(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	const secret = "my-private-question-text"
	for _, reply := range []func(w http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"rejected: ` + secret + `"}}`))
		},
		func(w http.ResponseWriter) {
			w.Write([]byte(`{"error":{"message":"policy: ` + secret + `"}}`))
		},
		func(w http.ResponseWriter) { w.Write([]byte(`not json ` + secret)) },
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; reply(w) }))
		h := newAskHandler("k", srv.URL+"/v1", "m", testApex, &askMemCounter{}, AskOptions{Burst: 10, Every: time.Second, DailyMax: 10, MaxInFlight: 1})
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/ask", h.ask)
		w := postAsk(mux, `{"question":"`+secret+`","page":"features"}`, nil)
		srv.Close()
		if w.Code != http.StatusBadGateway {
			t.Errorf("got %d, want 502", w.Code)
		}
		if calls != 1 {
			t.Errorf("upstream called %d times, want 1", calls)
		}
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("upstream text reached the log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "llm status 400") {
		t.Fatalf("status code not logged:\n%s", buf.String())
	}
}

// The daily count in Postgres survives a restart (a new handler) and stops at
// the cap. Needs ASK_TEST_DSN or MIGRATE_TEST_DSN; uses a temporary table, so
// it leaves the database as it found it.
func TestAskDailyCountInPostgres(t *testing.T) {
	dsn := os.Getenv("ASK_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("MIGRATE_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("ASK_TEST_DSN / MIGRATE_TEST_DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // the temporary table lives on one connection
	if _, err := db.Exec(`CREATE TEMP TABLE ask_daily (day DATE PRIMARY KEY, count INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := askDBCounter{db}
	for want := 1; want <= 2; want++ {
		n, ok, err := first.take(ctx, "2026-09-27", 3)
		if err != nil || !ok || n != want {
			t.Fatalf("take %d: %d %v %v", want, n, ok, err)
		}
	}
	restarted := askDBCounter{db}
	if n, ok, err := restarted.take(ctx, "2026-09-27", 3); err != nil || !ok || n != 3 {
		t.Fatalf("after restart: %d %v %v", n, ok, err)
	}
	if _, ok, err := restarted.take(ctx, "2026-09-27", 3); err != nil || ok {
		t.Fatalf("over the cap: ok=%v err=%v", ok, err)
	}
	if n, ok, err := restarted.take(ctx, "2026-09-28", 3); err != nil || !ok || n != 1 {
		t.Fatalf("next day: %d %v %v", n, ok, err)
	}
	if _, ok, _ := restarted.take(ctx, "2026-09-28", 0); ok {
		t.Fatalf("ASK_DAILY_MAX=0 must refuse every question")
	}
}

// The packs state the saved-data undo, the saved-data cap and the Recently
// deleted window in the served pages' words, so they follow the settings.
func TestAskPacksFollowLimits(t *testing.T) {
	if p := askPack("simple-host"); strings.Count(p, "30-day undo") < 2 {
		t.Fatalf("the pack at the defaults does not state the 30-day undo in the summary and the architecture section")
	}
	withLimits(t, map[string]string{"SAVED_DATA_UNDO_DAYS": "14", "SAVED_DATA_SITE_MAX_MB": "80", "DELETED_RETENTION_DAYS": "10"})
	p := askPackSection(askPack("simple-host"), "/architecture.html")
	for _, want := range []string{"Every change is kept for 14 days", "after 14 days it is gone for good", "14-day undo", "capped at 80 MB.", "Recently deleted for 10 days"} {
		if !strings.Contains(p, want) {
			t.Errorf("architecture pack with changed limits lacks %q", want)
		}
	}
	for _, old := range []string{"30 days", "30-day", "50 MB", "7 days"} {
		if strings.Contains(p, old) {
			t.Errorf("architecture pack with changed limits still says %q", old)
		}
	}
}
