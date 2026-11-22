package assistant

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

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	"github.com/vsriram/simple-host/host/llm"
)

// fakeProvider is a minimal OpenAI-compatible chat-completions endpoint
// (an httptest.Server), standing in for the configured ASSISTANT_BASE_URL
// so these tests exercise the real host/llm.Client and the real
// AssistantService, not a hand-rolled test double of either.
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

func defaultAssistantConfig() AssistantConfig {
	return AssistantConfig{
		Model:                 "test-model",
		MonthlyBudgetUSD:      5,
		DailyQuestions:        30,
		PriceInputPerMTokUSD:  0.5,
		PriceOutputPerMTokUSD: 2.0,
	}
}

// setAssistant wires a's HackHandler to a real AssistantService over
// a real *llm.Client pointed at fp, so a request really round-trips
// the assistant route -> AssistantService -> host/llm -> the fake HTTP
// provider and back.
func (a *hackApp) setAssistant(cfg AssistantConfig, fp *fakeProvider, docsText func() string) {
	cfg.BaseURL = fp.srv.URL
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	client := llm.New(llm.Config{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey,
		ReasoningEffort: cfg.ReasoningEffort,
		MaxTokens:       AssistantMaxTokens,
	})
	if docsText == nil {
		docsText = func() string { return "## Start /docs/start/\nGetting started with Simple Hack." }
	}
	a.as.setService(NewAssistant(a.DB, client, cfg, docsText))
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
	r := w.Call(t, "GET", "/assistant", nil, w.Org)
	if r.Status != 200 {
		t.Fatalf("GET off: %d %s", r.Status, r.Body)
	}
	if body := r.JSON(t); body["enabled"] != false {
		t.Fatalf("expected enabled:false, got %v", body)
	}
	r = w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.Org)
	if r.Status != http.StatusNotFound {
		t.Fatalf("POST off: %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
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
		p    hacktest.Person
	}{
		{"participant", w.Part.Person},
		{"judge", w.Judges[0].Person},
	} {
		r := w.Call(t, "GET", "/assistant", nil, tc.p)
		if r.Status != http.StatusForbidden {
			t.Fatalf("%s GET: %d %s", tc.name, r.Status, r.Body)
		}
		if body := r.JSON(t); body["code"] != "organisers_only" {
			t.Fatalf("%s GET body: %v", tc.name, body)
		}
		r = w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, tc.p)
		if r.Status != http.StatusForbidden {
			t.Fatalf("%s POST: %d %s", tc.name, r.Status, r.Body)
		}
	}

	// A platform admin inspecting an event it has not joined: loadMember's
	// usual admin bypass would otherwise treat this as organiser read access.
	r := w.a.At(t, "GET", w.Path("/assistant"), nil, w.a.AdminH())
	if r.Status != http.StatusForbidden {
		t.Fatalf("admin (not joined) GET: %d %s", r.Status, r.Body)
	}
	if body := r.JSON(t); body["code"] != "organisers_only" {
		t.Fatalf("admin GET body: %v", body)
	}

	// The organiser itself is unaffected.
	r = w.Call(t, "GET", "/assistant", nil, w.Org)
	if r.Status != 200 {
		t.Fatalf("organiser GET: %d %s", r.Status, r.Body)
	}

	// A complete non-member: loadMember's own 404, not this route's 403.
	r = w.Call(t, "GET", "/assistant", nil, w.Outsider)
	if r.Status != http.StatusNotFound {
		t.Fatalf("outsider GET: %d %s", r.Status, r.Body)
	}

	// Actual organiser membership wins over the platform-admin status
	// (same fact hack_checklists_test.go's TestChecklistMarksAuthorityAndFacts
	// exercises): once the admin has actually joined this event as
	// organiser, loadMember returns a real member row with admin=false
	// (the admin bypass fires only for a *missing* membership row), so it
	// must NOT be 403'd here.
	w.AddOrganiser(t, w.a.AdminH())
	r = w.a.At(t, "GET", w.Path("/assistant"), nil, w.a.AdminH())
	if r.Status != 200 {
		t.Fatalf("admin (joined organiser) GET: %d %s", r.Status, r.Body)
	}
}

func TestAssistantTakenDownEventRefuses(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"ok"}})
	w.a.setAssistant(defaultAssistantConfig(), fp, nil)

	if _, err := w.a.DB.Exec(`UPDATE events SET taken_down_at=now(), taken_down_reason='test' WHERE slug=$1`, w.Slug); err != nil {
		t.Fatal(err)
	}

	r := w.Call(t, "GET", "/assistant", nil, w.Org)
	if r.Status != http.StatusForbidden {
		t.Fatalf("GET: %d %s, want 403", r.Status, r.Body)
	}
	if body := r.JSON(t); body["code"] != "event_taken_down" {
		t.Fatalf("GET body: %v", body)
	}

	r = w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.Org)
	if r.Status != http.StatusForbidden {
		t.Fatalf("POST: %d %s, want 403", r.Status, r.Body)
	}
	if body := r.JSON(t); body["code"] != "event_taken_down" {
		t.Fatalf("POST body: %v", body)
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

	ask := func() hacktest.Resp {
		return w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.Org)
	}
	for i := 0; i < 2; i++ {
		r := ask()
		if r.Status != 200 {
			t.Fatalf("question %d: %d %s", i+1, r.Status, r.Body)
		}
	}
	r := ask()
	if r.Status != http.StatusTooManyRequests {
		t.Fatalf("3rd question: %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
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

	r := w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.Org)
	if r.Status != http.StatusTooManyRequests {
		t.Fatalf("got %d %s, want 429", r.Status, r.Body)
	}
	body := r.JSON(t)
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

	before := w.Call(t, "GET", "/assistant", nil, w.Org).JSON(t)
	r := w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}, w.Org)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("got %d %s, want 502", r.Status, r.Body)
	}
	if body := r.JSON(t); body["code"] != "provider_error" {
		t.Fatalf("error = %v", body["code"])
	}
	after := w.Call(t, "GET", "/assistant", nil, w.Org).JSON(t)
	if before["used_today"] != after["used_today"] {
		t.Fatalf("used_today changed: before %v after %v (refund expected)", before["used_today"], after["used_today"])
	}
}
