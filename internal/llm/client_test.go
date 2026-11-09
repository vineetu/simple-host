package llm

import (
	"context"
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

func sseServer(t *testing.T, write func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		write(w, body)
	}))
}

func writeSSE(w http.ResponseWriter, chunks ...string) {
	flusher, _ := w.(http.Flusher)
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func TestStreamDeltasInOrder(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		if body["stream"] != true {
			t.Errorf("expected stream:true in request, got %v", body["stream"])
		}
		so, _ := body["stream_options"].(map[string]any)
		if so == nil || so["include_usage"] != true {
			t.Errorf("expected stream_options.include_usage:true, got %v", body["stream_options"])
		}
		writeSSE(w,
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"reasoning":"thinking..."}}]}`,
			`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
			`[DONE]`,
		)
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "test-model", MaxTokens: 100})
	var got strings.Builder
	usage, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(delta string) {
		got.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.String() != "Hello" {
		t.Fatalf("got deltas %q, want %q", got.String(), "Hello")
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 2 {
		t.Fatalf("got usage %+v, want {10 2}", usage)
	}
}

func TestStreamIgnoresReasoningDelta(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		writeSSE(w,
			`{"choices":[{"delta":{"reasoning":"hmm"}}]}`,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`[DONE]`,
		)
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	var got strings.Builder
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(delta string) {
		got.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.String() != "ok" {
		t.Fatalf("got %q, want %q (reasoning delta must be ignored)", got.String(), "ok")
	}
}

func TestStreamReasoningEffortOmittedWhenEmpty(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		if _, present := body["reasoning_effort"]; present {
			t.Errorf("reasoning_effort should be absent when Config.ReasoningEffort is empty, got %v", body["reasoning_effort"])
		}
		writeSSE(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	if _, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

func TestStreamReasoningEffortSentWhenSet(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		if body["reasoning_effort"] != "low" {
			t.Errorf("expected reasoning_effort=low, got %v", body["reasoning_effort"])
		}
		writeSSE(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model", ReasoningEffort: "low"})
	if _, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

func TestStreamAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model", APIKey: "secret-key"})
	if _, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("got Authorization %q, want %q", gotAuth, "Bearer secret-key")
	}
}

func TestStreamNoAuthorizationHeaderWhenKeyEmpty(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	if _, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sawHeader {
		t.Fatalf("expected no Authorization header when APIKey is empty")
	}
}

func TestStreamUpstreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "bad gateway from upstream")
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {})
	if err == nil {
		t.Fatal("expected an error on a non-2xx status")
	}
}

func TestStreamClientCancelPropagates(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"a"}}]}`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		close(block)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	_, err := c.Stream(ctx, []Message{{Role: "user", Content: "hi"}}, func(string) {
		once.Do(cancel)
	})
	if err == nil {
		t.Fatal("expected an error when the caller cancels mid-stream")
	}
	select {
	case <-block:
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed the client cancellation")
	}
}

func TestStreamMissingBaseURLOrModel(t *testing.T) {
	c := New(Config{})
	if _, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err == nil {
		t.Fatal("expected an error with no BaseURL/Model")
	}
}

// TestStreamChunkErrorObject: an {"error":{"message":"..."}} chunk is an
// error, not a line to skip.
func TestStreamChunkErrorObject(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		writeSSE(w, `{"error":{"message":"rate limited"}}`)
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("got %v, want an error mentioning the provider's message", err)
	}
}

// TestStreamChunkErrorString: a bare {"error":"..."} chunk (no object) is
// also an error.
func TestStreamChunkErrorString(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		writeSSE(w, `{"error":"boom"}`)
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v, want an error mentioning \"boom\"", err)
	}
}

// TestStreamEOFWithoutDoneOrFinishReasonIsError: the connection closes
// cleanly (no transport error) but [DONE] never arrived and no choice ever
// carried a finish_reason -- a truncated stream, not a successful one.
func TestStreamEOFWithoutDoneOrFinishReasonIsError(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		writeSSE(w, `{"choices":[{"delta":{"content":"partial"}}]}`)
		// No [DONE], no finish_reason: the handler just returns, closing
		// the connection.
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	var got strings.Builder
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(d string) { got.WriteString(d) })
	if err == nil {
		t.Fatal("expected an error when the stream ends without [DONE] or a finish_reason")
	}
	if got.String() != "partial" {
		t.Fatalf("expected the partial delta to still have been delivered, got %q", got.String())
	}
}

// TestStreamFinishReasonWithoutDoneIsOK: a real finish_reason is enough to
// treat the stream as complete even if the provider never sends [DONE].
func TestStreamFinishReasonWithoutDoneIsOK(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, body map[string]any) {
		writeSSE(w,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

// TestStreamUpstreamErrorStatusNeverEchoesBody: the error for a non-2xx
// status never carries the response body (which may hold provider detail
// that should never be logged -- see events.Ask's refusal to log bodies).
func TestStreamUpstreamErrorStatusNeverEchoesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "SENTINEL-UPSTREAM-BODY-TEXT")
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "test-model"})
	_, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {})
	if err == nil {
		t.Fatal("expected an error on a non-2xx status")
	}
	if strings.Contains(err.Error(), "SENTINEL-UPSTREAM-BODY-TEXT") {
		t.Fatalf("error must not echo the upstream response body: %v", err)
	}
}
