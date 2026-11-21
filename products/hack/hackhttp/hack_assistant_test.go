package hackhttp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/llm"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// fakeProvider is a minimal OpenAI-compatible chat-completions endpoint
// (an httptest.Server), standing in for the configured ASSISTANT_BASE_URL
// so these tests exercise the real host/llm.Client and the real
// events.AssistantService, not a hand-rolled test double of either.
type fakeProvider struct {
	srv *httptest.Server
	// lastMessages is the "messages" array of the most recent request, as
	// decoded JSON (so the system prompt's content is inspectable).
	lastMessages chan []map[string]any
	// canceled is closed when a blockThenFail handler observes its own
	// request context done -- proof the client's cancellation actually
	// reached the upstream HTTP request, not just that the client gave up
	// reading.
	canceled chan struct{}
}

type fakeProviderPlan struct {
	deltas          []string
	usage           map[string]int
	failImmediately bool
	blockThenFail   bool // write one delta, then block until the client disconnects
}

func newFakeProvider(t *testing.T, plan fakeProviderPlan) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{lastMessages: make(chan []map[string]any, 8), canceled: make(chan struct{})}
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if msgs, ok := body["messages"].([]any); ok {
			var out []map[string]any
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok {
					out = append(out, mm)
				}
			}
			select {
			case fp.lastMessages <- out:
			default:
			}
		}
		if plan.failImmediately {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream exploded")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		writeChunk := func(v map[string]any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		for _, d := range plan.deltas {
			writeChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": d}}}})
		}
		if plan.blockThenFail {
			<-r.Context().Done()
			close(fp.canceled)
			return
		}
		chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}}}}
		if plan.usage != nil {
			chunk["usage"] = plan.usage
		}
		writeChunk(chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(fp.srv.Close)
	return fp
}

func defaultAssistantConfig() events.AssistantConfig {
	return events.AssistantConfig{
		Model:                 "test-model",
		MonthlyBudgetUSD:      5,
		DailyQuestions:        30,
		PriceInputPerMTokUSD:  0.5,
		PriceOutputPerMTokUSD: 2.0,
	}
}

// setAssistant wires a's HackHandler to a real events.AssistantService over
// a real *llm.Client pointed at fp, so a request really round-trips
// hackhttp -> events.AssistantService -> host/llm -> the fake HTTP
// provider and back.
func (a *hackApp) setAssistant(cfg events.AssistantConfig, fp *fakeProvider, docsText func() string) {
	cfg.BaseURL = fp.srv.URL
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	client := llm.New(llm.Config{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey,
		ReasoningEffort: cfg.ReasoningEffort,
		MaxTokens:       events.AssistantMaxTokens,
	})
	if docsText == nil {
		docsText = func() string { return "## Start /docs/start/\nGetting started with Simple Hack." }
	}
	a.hack.SetAssistant(events.NewAssistant(a.database, client, cfg, docsText))
}

// readSSE splits a complete SSE body (as a.at already read it fully) into
// its decoded "data: {...}" JSON events, in order.
func readSSE(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			t.Fatalf("bad SSE event %q: %v", payload, err)
		}
		events = append(events, v)
	}
	return events
}

// TestAssistantOffByDefault: no SetAssistant call (ASSISTANT_BASE_URL/MODEL
// unset) -- GET says enabled:false, POST refuses 404 assistant_off.
func TestAssistantOffByDefault(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	r := w.call(t, "GET", "/assistant", nil, w.org)
	if r.status != 200 {
		t.Fatalf("GET off: %d %s", r.status, r.body)
	}
	if body := r.json(t); body["enabled"] != false {
		t.Fatalf("expected enabled:false, got %v", body)
	}
	r = w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.org)
	if r.status != http.StatusNotFound {
		t.Fatalf("POST off: %d %s", r.status, r.body)
	}
	body := r.json(t)
	if body["code"] != "assistant_off" {
		t.Fatalf("expected error=assistant_off, got %v", body)
	}
}

// TestAssistantOrganisersOnly: a participant, a judge and a platform admin
// inspecting an event it never joined all get 403 organisers_only; the
// organiser itself does not. A complete non-member (never joined this
// event in any role) gets loadMember's usual 404 event_not_found, same as
// every other hack route -- that is the existing hackAccess contract
// ("Non-members get 404 event_not_found"), not specific to this route.
func TestAssistantOrganisersOnly(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"ok"}})
	w.a.setAssistant(defaultAssistantConfig(), fp, nil)

	for _, tc := range []struct {
		name string
		p    person
	}{
		{"participant", w.part.person},
		{"judge", w.judges[0].person},
	} {
		r := w.call(t, "GET", "/assistant", nil, tc.p)
		if r.status != http.StatusForbidden {
			t.Fatalf("%s GET: %d %s", tc.name, r.status, r.body)
		}
		if body := r.json(t); body["code"] != "organisers_only" {
			t.Fatalf("%s GET body: %v", tc.name, body)
		}
		r = w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, tc.p)
		if r.status != http.StatusForbidden {
			t.Fatalf("%s POST: %d %s", tc.name, r.status, r.body)
		}
	}

	// A platform admin inspecting an event it has not joined: loadMember's
	// usual admin bypass would otherwise treat this as organiser read access.
	r := w.a.at(t, "GET", w.path("/assistant"), nil, w.a.adminH())
	if r.status != http.StatusForbidden {
		t.Fatalf("admin (not joined) GET: %d %s", r.status, r.body)
	}
	if body := r.json(t); body["code"] != "organisers_only" {
		t.Fatalf("admin GET body: %v", body)
	}

	// The organiser itself is unaffected.
	r = w.call(t, "GET", "/assistant", nil, w.org)
	if r.status != 200 {
		t.Fatalf("organiser GET: %d %s", r.status, r.body)
	}

	// A complete non-member: loadMember's own 404, not this route's 403.
	r = w.call(t, "GET", "/assistant", nil, w.outsider)
	if r.status != http.StatusNotFound {
		t.Fatalf("outsider GET: %d %s", r.status, r.body)
	}

	// Actual organiser membership wins over the platform-admin status
	// (same fact hack_checklists_test.go's TestChecklistMarksAuthorityAndFacts
	// exercises): once the admin has actually joined this event as
	// organiser, loadMember returns a real member row with admin=false
	// (the admin bypass fires only for a *missing* membership row), so it
	// must NOT be 403'd here.
	w.addOrganiser(t, w.a.adminH())
	r = w.a.at(t, "GET", w.path("/assistant"), nil, w.a.adminH())
	if r.status != 200 {
		t.Fatalf("admin (joined organiser) GET: %d %s", r.status, r.body)
	}
}

// TestAssistantTakenDownEventRefuses: a taken-down event's organiser gets
// 403 event_taken_down from both routes, without ever reaching the
// provider (adversarial review, SIM-394) -- loadMember only applies this
// check for write=true, and the assistant route calls it with write=false
// (it needs the plain, non-404-masking loadMember behaviour for its own
// 403 organisers_only), so assistantAccess checks TakenDown itself.
// TestSanitizeAssistantEventViewJSONCatchesTypedValues: the sanitiser's
// recursion only matches generic map[string]any/[]any shapes, so a value
// built from concrete Go types -- a typed struct, a []map[string]any --
// would otherwise pass straight through untouched. The marshal/unmarshal
// round trip (adversarial review, SIM-394) canonicalises everything into
// those generic shapes first, so a secret-looking field name is caught no
// matter what Go type originally carried it.
func TestSanitizeAssistantEventViewJSONCatchesTypedValues(t *testing.T) {
	type needsYouRow struct {
		JoinCode     string `json:"join_code"`
		ContactEmail string `json:"contact_email"`
		Label        string `json:"label"`
	}
	type eventViewLike struct {
		NeedsYou []needsYouRow  `json:"needs_you"`
		Counts   map[string]int `json:"counts"`
		Title    string         `json:"title"`
	}
	view := eventViewLike{
		NeedsYou: []needsYouRow{{JoinCode: "ABC123", ContactEmail: "a@b.c", Label: "Approve Pat"}},
		Counts:   map[string]int{"teams": 3},
		Title:    "Spring hack",
	}
	raw, err := sanitizeAssistantEventViewJSON(view)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "ABC123") || strings.Contains(s, "join_code") {
		t.Fatalf("join_code leaked from a typed struct field: %s", s)
	}
	if strings.Contains(s, "a@b.c") || strings.Contains(s, "contact_email") {
		t.Fatalf("contact_email leaked from a typed struct field: %s", s)
	}
	if !strings.Contains(s, "Approve Pat") || !strings.Contains(s, "Spring hack") || !strings.Contains(s, `"teams":3`) {
		t.Fatalf("non-secret fields were dropped too: %s", s)
	}
}

func TestAssistantTakenDownEventRefuses(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"ok"}})
	w.a.setAssistant(defaultAssistantConfig(), fp, nil)

	if _, err := w.a.database.Exec(`UPDATE events SET taken_down_at=now(), taken_down_reason='test' WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}

	r := w.call(t, "GET", "/assistant", nil, w.org)
	if r.status != http.StatusForbidden {
		t.Fatalf("GET: %d %s, want 403", r.status, r.body)
	}
	if body := r.json(t); body["code"] != "event_taken_down" {
		t.Fatalf("GET body: %v", body)
	}

	r = w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.org)
	if r.status != http.StatusForbidden {
		t.Fatalf("POST: %d %s, want 403", r.status, r.body)
	}
	if body := r.json(t); body["code"] != "event_taken_down" {
		t.Fatalf("POST body: %v", body)
	}
}

// TestAssistantStreamingPromptAndRemaining: a normal question streams its
// deltas in order, the final event carries remaining_today, and the system
// prompt the fake provider received resolves a real organiser href, embeds
// the checklist/event JSON, the injected docs text, and none of the
// event's join/judge codes.
func TestAssistantStreamingPromptAndRemaining(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"You can ", "set it up [here](/e/" + w.slug + "/manage?s=overview)."}, usage: map[string]int{"prompt_tokens": 120, "completion_tokens": 20}})
	cfg := defaultAssistantConfig()
	cfg.DailyQuestions = 30
	w.a.setAssistant(cfg, fp, func() string { return "## Start /docs/start/\nMARKER-DOCS-TEXT getting started." })

	r := w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "Is my setup done?"}}}, w.org)
	if r.status != 200 {
		t.Fatalf("POST: %d %s", r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: %q", ct)
	}
	if cc := r.header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control: %q", cc)
	}
	if xb := r.header.Get("X-Accel-Buffering"); xb != "no" {
		t.Fatalf("x-accel-buffering: %q", xb)
	}
	evs := readSSE(t, r.body)
	if len(evs) != 3 {
		t.Fatalf("expected 2 deltas + done, got %d: %+v", len(evs), evs)
	}
	got := ""
	for _, e := range evs[:2] {
		d, _ := e["delta"].(string)
		if d == "" {
			t.Fatalf("expected a delta event, got %v", e)
		}
		got += d
	}
	want := "You can set it up [here](/e/" + w.slug + "/manage?s=overview)."
	if got != want {
		t.Fatalf("deltas = %q, want %q", got, want)
	}
	last := evs[2]
	if last["done"] != true {
		t.Fatalf("last event not done: %v", last)
	}
	if n, ok := last["remaining_today"].(float64); !ok || int(n) != 29 {
		t.Fatalf("remaining_today = %v, want 29", last["remaining_today"])
	}

	var msgs []map[string]any
	select {
	case msgs = <-fp.lastMessages:
	case <-time.After(2 * time.Second):
		t.Fatal("fake provider never received a request")
	}
	if len(msgs) < 2 || msgs[0]["role"] != "system" {
		t.Fatalf("expected a system message first: %+v", msgs)
	}
	sys, _ := msgs[0]["content"].(string)
	wantHref := "/e/" + w.slug + "/manage?s=overview"
	if !strings.Contains(sys, wantHref) {
		t.Fatalf("system prompt missing resolved organiser href %q:\n%s", wantHref, sys)
	}
	if !strings.Contains(sys, "MARKER-DOCS-TEXT") {
		t.Fatalf("system prompt missing injected docs text:\n%s", sys)
	}
	if !strings.Contains(sys, "\"checklist\"") {
		t.Fatalf("system prompt missing the event view's checklist:\n%s", sys)
	}
	if !strings.Contains(sys, "Spring hack") {
		t.Fatalf("system prompt missing the event's own title:\n%s", sys)
	}
	for _, secret := range []string{"join_code", "judge_code", "join_url", "judge_url"} {
		if strings.Contains(sys, secret) {
			t.Fatalf("system prompt leaked %q:\n%s", secret, sys)
		}
	}
	// The real values, not just the key names: join_url/judge_url embed
	// the exact same code join_code/judge_code carry bare, so stripping
	// the key but not the URL would leak the working invite link anyway.
	orgView := w.a.at(t, "GET", w.path(""), nil, w.a.key(w.org)).json(t)["organiser"].(map[string]any)
	for _, field := range []string{"join_code", "judge_code"} {
		code, _ := orgView[field].(string)
		if code == "" {
			t.Fatalf("missing %s in organiser view: %v", field, orgView)
		}
		if strings.Contains(sys, code) {
			t.Fatalf("system prompt leaked the raw %s value %q:\n%s", field, code, sys)
		}
	}
}

// TestAssistantDailyLimit: once an organiser has asked DailyQuestions
// questions today, the next one is refused with 429 daily_limit and
// remaining_today:0, without ever reaching the provider.
func TestAssistantDailyLimit(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"ok"}})
	cfg := defaultAssistantConfig()
	cfg.DailyQuestions = 2
	w.a.setAssistant(cfg, fp, nil)

	ask := func() resp {
		return w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.org)
	}
	for i := 0; i < 2; i++ {
		r := ask()
		if r.status != 200 {
			t.Fatalf("question %d: %d %s", i+1, r.status, r.body)
		}
	}
	r := ask()
	if r.status != http.StatusTooManyRequests {
		t.Fatalf("3rd question: %d %s", r.status, r.body)
	}
	body := r.json(t)
	if body["code"] != "daily_limit" {
		t.Fatalf("error = %v", body["code"])
	}
	if n, ok := body["remaining_today"].(float64); !ok || n != 0 {
		t.Fatalf("remaining_today = %v", body["remaining_today"])
	}
}

// TestAssistantMonthlyBudget: a budget too small for even one question's
// worst-case reservation refuses with 429 monthly_budget and a resets date,
// without reaching the provider.
func TestAssistantMonthlyBudget(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"ok"}})
	cfg := defaultAssistantConfig()
	cfg.MonthlyBudgetUSD = 0 // any positive reservation exceeds a zero budget
	w.a.setAssistant(cfg, fp, nil)

	r := w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.org)
	if r.status != http.StatusTooManyRequests {
		t.Fatalf("got %d %s, want 429", r.status, r.body)
	}
	body := r.json(t)
	if body["code"] != "monthly_budget" {
		t.Fatalf("error = %v", body["code"])
	}
	resets, _ := body["resets"].(string)
	if resets == "" {
		t.Fatalf("missing resets date: %v", body)
	}
	if _, err := time.Parse("2006-01-02", resets); err != nil {
		t.Fatalf("resets %q not YYYY-MM-DD: %v", resets, err)
	}
}

// TestAssistantProviderFailureRefunds: a provider failure before any delta
// answers 502 provider_error and refunds the question (the daily count is
// unchanged by GET /assistant before vs after).
func TestAssistantProviderFailureRefunds(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{failImmediately: true})
	w.a.setAssistant(defaultAssistantConfig(), fp, nil)

	before := w.call(t, "GET", "/assistant", nil, w.org).json(t)
	r := w.call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.org)
	if r.status != http.StatusBadGateway {
		t.Fatalf("got %d %s, want 502", r.status, r.body)
	}
	if body := r.json(t); body["code"] != "provider_error" {
		t.Fatalf("error = %v", body["code"])
	}
	after := w.call(t, "GET", "/assistant", nil, w.org).json(t)
	if before["used_today"] != after["used_today"] {
		t.Fatalf("used_today changed: before %v after %v (refund expected)", before["used_today"], after["used_today"])
	}
}
