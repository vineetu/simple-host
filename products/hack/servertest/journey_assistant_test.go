package servertest

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
	"github.com/vsriram/simple-host/products/hack/assistant"
)

// TestAssistantStreamingPromptAndRemaining: a normal question streams its
// deltas in order, the final event carries remaining_today, and the system
// prompt the fake provider received resolves a real organiser href, embeds
// the checklist/event JSON, the injected docs text, and none of the
// event's join/judge codes.
func TestAssistantStreamingPromptAndRemaining(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	fp := newFakeProvider(t, fakeProviderPlan{deltas: []string{"You can ", "set it up [here](/e/" + w.Slug + "/manage?s=overview)."}, usage: map[string]int{"prompt_tokens": 120, "completion_tokens": 20}})
	cfg := defaultAssistantConfig()
	cfg.DailyQuestions = 30
	w.a.setAssistant(cfg, fp, func() string { return "## Start /docs/start/\nMARKER-DOCS-TEXT getting started." })

	r := w.Call(t, "POST", "/assistant", map[string]any{"messages": []map[string]string{{"role": "user", "content": "Is my setup done?"}}}, w.Org)
	if r.Status != 200 {
		t.Fatalf("POST: %d %s", r.Status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: %q", ct)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control: %q", cc)
	}
	if xb := r.Header.Get("X-Accel-Buffering"); xb != "no" {
		t.Fatalf("x-accel-buffering: %q", xb)
	}
	evs := readSSE(t, r.Body)
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
	want := "You can set it up [here](/e/" + w.Slug + "/manage?s=overview)."
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
	wantHref := "/e/" + w.Slug + "/manage?s=overview"
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
	orgView := w.a.At(t, "GET", w.Path(""), nil, w.a.Key(w.Org)).JSON(t)["organiser"].(map[string]any)
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

type fakeProviderPlan struct {
	deltas          []string
	usage           map[string]int
	failImmediately bool
	blockThenFail   bool // write one delta, then block until the client disconnects
}

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

func defaultAssistantConfig() assistant.AssistantConfig {
	return assistant.AssistantConfig{
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
func (a *featApp) setAssistant(cfg assistant.AssistantConfig, fp *fakeProvider, docsText func() string) {
	cfg.BaseURL = fp.srv.URL
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	client := llm.New(llm.Config{
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		APIKey:          cfg.APIKey,
		ReasoningEffort: cfg.ReasoningEffort,
		MaxTokens:       assistant.AssistantMaxTokens,
	})
	if docsText == nil {
		docsText = func() string { return "## Start /docs/start/\nGetting started with Simple Hack." }
	}
	a.hack.SetAssistant(assistant.NewAssistant(a.DB, client, cfg, docsText))
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
