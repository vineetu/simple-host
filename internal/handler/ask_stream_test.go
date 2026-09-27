package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamSidecar streams deltas the way the sidecar does with "stream": true.
// wait holds the first delta back; each delta is flushed on its own.
type streamSidecar struct {
	deltas []string
	finish string
	wait   time.Duration
	hold   chan struct{} // if set, the stream stops after the first delta until closed
	mu     sync.Mutex
	bodies []string
	gone   chan struct{} // closed when the upstream request's context ends
}

func (f *streamSidecar) server(t *testing.T) *httptest.Server {
	f.gone = make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		f.mu.Unlock()
		defer once.Do(func() { close(f.gone) })
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		select {
		case <-time.After(f.wait):
		case <-r.Context().Done():
			return
		}
		for i, d := range f.deltas {
			c := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": d}}}}
			if i == len(f.deltas)-1 && f.finish != "" {
				c = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": d}, "finish_reason": f.finish}}}
			}
			bb, _ := json.Marshal(c)
			fmt.Fprintf(w, "data: %s\n\n", bb)
			fl.Flush()
			if i == 0 && f.hold != nil {
				select {
				case <-f.hold:
				case <-r.Context().Done():
					return
				}
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newStreamAsk(t *testing.T, f *streamSidecar, o AskOptions) (*AskHandler, *http.ServeMux) {
	srv := f.server(t)
	if o.Burst == 0 {
		o = AskOptions{Burst: 100, Every: time.Second, DailyMax: 100, MaxInFlight: 4, ReasoningEffort: o.ReasoningEffort, MaxTokens: o.MaxTokens}
	}
	h := newAskHandler("k", srv.URL+"/v1", "grok-4.7", testApex, &askMemCounter{}, o)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", h.ask)
	return h, mux
}

// sseEvents reads a text/event-stream body into its data payloads.
func sseEvents(t *testing.T, body io.Reader) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line[6:]), &m); err != nil {
			t.Fatalf("event %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func postAskStream(mux http.Handler, body string) *httptest.ResponseRecorder {
	return postAsk(mux, body, func(r *http.Request) { r.Header.Set("Accept", "text/event-stream") })
}

// Deltas are relayed in order, then one final event with the cleaned answer.
func TestAskStreamRelaysDeltasThenCleanAnswer(t *testing.T) {
	f := &streamSidecar{deltas: []string{"It keeps **saved", " data** in ", "Postgres. See [x](https://evil.example/a)."}}
	_, mux := newStreamAsk(t, f, AskOptions{})
	w := postAskStream(mux, `{"question":"where is data?","page":"features"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering: %q", w.Header().Get("X-Accel-Buffering"))
	}
	ev := sseEvents(t, w.Body)
	if len(ev) != 4 {
		t.Fatalf("events: %v", ev)
	}
	for i, d := range f.deltas {
		if ev[i]["t"] != d {
			t.Errorf("event %d: %v, want delta %q", i, ev[i], d)
		}
	}
	last := ev[3]
	if last["done"] != true || last["answer"] != "It keeps saved data in Postgres. See x." {
		t.Fatalf("final event %v", last)
	}
}

// The JSON mode (no Accept: text/event-stream) still answers in one piece.
func TestAskStreamJSONModeStillWorks(t *testing.T) {
	f := &streamSidecar{deltas: []string{"Yes, ", "it does."}}
	_, mux := newStreamAsk(t, f, AskOptions{})
	w := postAsk(mux, `{"question":"q","page":"features"}`, nil)
	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got["answer"] != "Yes, it does." {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

// Model, reasoning effort, max tokens and stream come from the knobs; an
// empty effort leaves the field out.
func TestAskSendsModelAndEffortFromKnobs(t *testing.T) {
	f := &streamSidecar{deltas: []string{"ok"}}
	_, mux := newStreamAsk(t, f, AskOptions{ReasoningEffort: "none", MaxTokens: 321})
	postAskStream(mux, `{"question":"q","page":"features"}`)
	var sent map[string]any
	json.Unmarshal([]byte(f.bodies[0]), &sent)
	if sent["model"] != "grok-4.7" || sent["reasoning_effort"] != "none" || sent["max_tokens"] != float64(321) || sent["stream"] != true {
		t.Fatalf("sent %v", sent)
	}

	f2 := &streamSidecar{deltas: []string{"ok"}}
	_, mux2 := newStreamAsk(t, f2, AskOptions{})
	postAsk(mux2, `{"question":"q","page":"features"}`, nil)
	var sent2 map[string]any
	json.Unmarshal([]byte(f2.bodies[0]), &sent2)
	if _, ok := sent2["reasoning_effort"]; ok {
		t.Fatalf("empty effort was sent: %v", sent2)
	}
	if sent2["max_tokens"] != float64(300) {
		t.Fatalf("default max tokens: %v", sent2["max_tokens"])
	}
}

// No text within the first-token timeout: a plain 502, and the upstream
// request is cancelled.
func TestAskStreamFirstTokenTimeout(t *testing.T) {
	f := &streamSidecar{deltas: []string{"late"}, wait: 5 * time.Second}
	h, mux := newStreamAsk(t, f, AskOptions{})
	h.firstToken = 100 * time.Millisecond
	start := time.Now()
	w := postAskStream(mux, `{"question":"q","page":"features"}`)
	if w.Code != http.StatusBadGateway || askCode(t, w) != "unavailable" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	select {
	case <-f.gone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request not cancelled")
	}
}

// The whole answer is bounded too: past total, the stream ends with an error
// event after the text already sent.
func TestAskStreamTotalTimeout(t *testing.T) {
	f := &streamSidecar{deltas: []string{"first", " second"}, hold: make(chan struct{})}
	defer close(f.hold)
	h, mux := newStreamAsk(t, f, AskOptions{})
	h.total = 200 * time.Millisecond
	w := postAskStream(mux, `{"question":"q","page":"features"}`)
	ev := sseEvents(t, w.Body)
	if len(ev) != 2 || ev[0]["t"] != "first" || ev[1]["code"] != "unavailable" {
		t.Fatalf("events %v", ev)
	}
}

// A reader who closes the panel (disconnects) cancels the model request.
func TestAskStreamClientDisconnectCancelsUpstream(t *testing.T) {
	f := &streamSidecar{deltas: []string{"first", " second"}, hold: make(chan struct{})}
	defer close(f.hold)
	_, mux := newStreamAsk(t, f, AskOptions{})
	front := httptest.NewServer(mux)
	defer front.Close()
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/ask", strings.NewReader(`{"question":"q","page":"features"}`))
	req.Header.Set("Origin", testApex)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Read the first event, so the stream is under way, then hang up.
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, `"first"`) {
		t.Fatalf("first line %q %v", line, err)
	}
	resp.Body.Close()
	select {
	case <-f.gone:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request still open after the reader left")
	}
}

// A reply cut at max_tokens ends with "…".
func TestAskStreamLengthCutMarked(t *testing.T) {
	f := &streamSidecar{deltas: []string{"A long", " answer"}, finish: "length"}
	_, mux := newStreamAsk(t, f, AskOptions{})
	ev := sseEvents(t, postAskStream(mux, `{"question":"q","page":"features"}`).Body)
	if ev[len(ev)-1]["answer"] != "A long answer…" {
		t.Fatalf("events %v", ev)
	}
}

// A follow-up carries at most askMaxTurns earlier turns (the latest), each
// cut to length, as user/assistant messages before the question.
func TestAskHistoryCapped(t *testing.T) {
	f := &streamSidecar{deltas: []string{"ok"}}
	_, mux := newStreamAsk(t, f, AskOptions{})
	var hist []askTurn
	for i := 1; i <= 6; i++ {
		hist = append(hist, askTurn{Q: fmt.Sprintf("q%d", i), A: fmt.Sprintf("a%d", i)})
	}
	hist[5].A = strings.Repeat("x", askMaxHistoryAnswerChars+500)
	hist[4].Q = "  "
	body, _ := json.Marshal(map[string]any{"question": "tell me more", "page": "features", "history": hist})
	if w := postAskStream(mux, string(body)); w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var sent struct {
		Messages []openAIMessage `json:"messages"`
	}
	json.Unmarshal([]byte(f.bodies[0]), &sent)
	var roles, contents []string
	for _, m := range sent.Messages[1:] {
		roles = append(roles, m.Role)
		c := m.Content
		if len(c) > 100 {
			c = fmt.Sprintf("x*%d", len(c))
		}
		contents = append(contents, c)
	}
	want := fmt.Sprintf("q3 a3 q4 a4 q6 x*%d tell me more", askMaxHistoryAnswerChars)
	if got := strings.Join(contents, " "); got != want {
		t.Fatalf("messages %q, want %q", got, want)
	}
	if strings.Join(roles, ",") != "user,assistant,user,assistant,user,assistant,user" {
		t.Fatalf("roles %v", roles)
	}
	if sent.Messages[0].Role != "system" {
		t.Fatalf("first message %v", sent.Messages[0].Role)
	}
}

// The system prompt asks for short answers unless more is asked for.
func TestAskPromptAsksForShortAnswers(t *testing.T) {
	p := askSystemPrompt("features")
	for _, want := range []string{"1 to 3 short sentences", "explicitly asks for more detail", "about 150 words"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}
