// Package llm is a tiny OpenAI-compatible chat-completions client (net/http
// only, no third-party SDK). It exists for SIM-394's hack organiser
// assistant, which talks to a configurable OpenAI-compatible endpoint (today,
// DeepSeek hosted on Ollama Cloud). It is infrastructure: no imports of any
// other internal package (scripts/layering/rules.txt).
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Message is one chat turn, OpenAI's {"role":"...","content":"..."} shape.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage is the token accounting an OpenAI-compatible endpoint reports on the
// final streamed chunk when stream_options.include_usage is set.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Config is one endpoint's connection details. BaseURL is the API root (e.g.
// "https://ollama.com/v1"); the client posts to BaseURL+"/chat/completions".
// APIKey is optional (sent as a Bearer token when set). ReasoningEffort is
// sent as the request field "reasoning_effort" only when non-empty: the
// hosted DeepSeek model on Ollama Cloud is a reasoning model, and
// reasoning_effort:"none" turns its thinking off, but other OpenAI-compatible
// providers may reject a request carrying a field they don't recognize, so
// the field is omitted entirely rather than sent empty.
type Config struct {
	BaseURL         string
	Model           string
	APIKey          string
	ReasoningEffort string
	MaxTokens       int
}

// Client is a configured connection to one OpenAI-compatible endpoint. Safe
// for concurrent use (http.Client is).
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. The response-header timeout bounds the time between
// the request being sent and the response headers arriving (15s, per the
// contract); Stream separately bounds the whole call including streaming
// the body (90s). A caller's own ctx deadline, if shorter, still wins. The
// transport is a clone of http.DefaultTransport (not a bare &http.Transport{},
// which would drop the defaults' proxy, dialer and connection-pooling
// settings) with only ResponseHeaderTimeout set.
func New(cfg Config) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &Client{
		cfg:  cfg,
		http: &http.Client{Transport: transport},
	}
}

// chatRequest is the OpenAI-compatible request body.
type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []Message      `json:"messages"`
	Stream          bool           `json:"stream"`
	StreamOptions   *streamOptions `json:"stream_options,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Temperature     float64        `json:"temperature"` // always 0: the same question gets the same answer
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatChunk is one SSE "data: {...}" line's JSON body.
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning,omitempty"`
		} `json:"delta"`
		// FinishReason is nil until the final chunk for a choice, which
		// carries a real value (e.g. "stop"). Used to tell a clean EOF
		// (stream ended normally, [DONE] just never arrived) from a
		// truncated one (see the EOF handling below).
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	// Error carries an OpenAI-compatible error chunk, either an object
	// ({"error":{"message":"..."}}) or a bare string ({"error":"..."}).
	Error json.RawMessage `json:"error,omitempty"`
}

// chunkError reports the error an "error" field in a chat chunk carries, if
// any (nil for an absent or JSON-null field). The field is either an object
// with a "message", or a bare string -- both seen across OpenAI-compatible
// providers.
func chunkError(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return fmt.Errorf("llm: provider error: %s", obj.Message)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return fmt.Errorf("llm: provider error: %s", s)
	}
	return fmt.Errorf("llm: provider error: %s", string(raw))
}

// Stream posts messages to the chat-completions endpoint with stream:true
// and calls onDelta for every piece of assistant text as it arrives, in
// order. It returns the token usage from the final chunk (zero value if the
// provider never sent one) and the first error encountered. A caller
// canceling ctx (client disconnect) surfaces as ctx.Err() here; the HTTP
// request carries the same ctx, so the upstream call is canceled too.
//
// Never logs the API key or the request body.
func (c *Client) Stream(ctx context.Context, messages []Message, onDelta func(string)) (Usage, error) {
	var usage Usage
	if c.cfg.BaseURL == "" || c.cfg.Model == "" {
		return usage, errors.New("llm: BaseURL and Model are required")
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	reqBody := chatRequest{
		Model:           c.cfg.Model,
		Messages:        messages,
		Stream:          true,
		StreamOptions:   &streamOptions{IncludeUsage: true},
		MaxTokens:       c.cfg.MaxTokens,
		ReasoningEffort: c.cfg.ReasoningEffort,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return usage, fmt.Errorf("llm: encode request: %w", err)
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return usage, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return usage, ctx.Err()
		}
		return usage, fmt.Errorf("llm: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never echo the response body: it is provider content (never
		// logged or surfaced -- see Ask's "never the key or bodies" rule),
		// and may itself carry sensitive detail.
		return usage, fmt.Errorf("llm: upstream status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	sawFinish := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return usage, nil
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Skip a line we can't parse rather than failing a whole answer
			// over one malformed chunk; keep going.
			continue
		}
		if err := chunkError(chunk.Error); err != nil {
			return usage, err
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				onDelta(choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				sawFinish = true
			}
		}
		if chunk.Usage != nil {
			usage = Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return usage, ctx.Err()
		}
		return usage, fmt.Errorf("llm: read stream: %w", err)
	}
	// The connection closed cleanly (scanner.Err() == nil) without a [DONE]
	// sentinel. That is fine when some choice's finish_reason already told
	// us the answer was complete; otherwise the stream was cut short
	// (truncated response, provider-side disconnect) and the caller should
	// treat it as a failure rather than silently accept a partial answer.
	if !sawFinish {
		return usage, errors.New("llm: stream ended without [DONE] or a finish_reason")
	}
	return usage, nil
}
