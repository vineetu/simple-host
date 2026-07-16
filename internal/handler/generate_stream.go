package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
)

// streamClient has NO client-side timeout: a full build can stream for minutes
// and the blocking h.client's 120s cap would sever it. Cancellation instead rides
// on the request context (r.Context()), which the http server cancels when the
// browser disconnects, propagating to the upstream DeepSeek request and unblocking
// the read loop.
var streamClient = &http.Client{}

// openaiStreamChunk is one SSE frame from an OpenAI-compatible streaming response
// (DeepSeek): {"choices":[{"delta":{"content":"…"}}]}.
type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// generateStream is the SSE variant of generate for the OpenAI-compatible direct
// path (DeepSeek). It streams the assistant reply token-by-token, then the HTML
// (delimited by siteHTMLSentinel), then a final authoritative "done" event that
// carries the canonical reply + cleaned HTML. The client treats deltas as UX and
// trusts "done" as the source of truth. Powers the /dev streaming-chat testbed.
func (h *GenerateHandler) generateStream(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "sign in to use AI create"})
		return
	}
	if !h.ipLimiter.allow(clientIP(r)) || !h.userLimiter.allow(user.ID) {
		tooManyRequests(w)
		return
	}
	// Streaming uses its own decoupled OpenAI-compatible backend (ConfigureStreaming).
	if h.streamAPIKey == "" || (h.streamProvider != "deepseek" && h.streamProvider != "openai") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "streaming not available for this backend"})
		return
	}

	var req generateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	msgs := sanitizeMessages(req.Messages)
	if len(msgs) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "say what you'd like to build"})
		return
	}
	atts, err := sanitizeAttachments(req.Attachments)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if len(atts) > 0 {
		li := len(msgs) - 1
		msgs[li].Content = buildUserBlocks(msgs[li].Content, atts)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "streaming unsupported"})
		return
	}
	// SSE headers. X-Accel-Buffering:no asks nginx not to buffer; whether the
	// shared proxy honors it is the go/no-go we test through the real proxy.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(ev map[string]any) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	send(map[string]any{"type": "stage", "value": "connecting"})

	if err := h.streamConverse(r.Context(), msgs, req.HTML, send); err != nil {
		log.Printf("generate/stream: %v", err)
		send(map[string]any{"type": "error", "message": "the assistant had trouble — please try again"})
	}
}

// streamConverse calls DeepSeek with stream:true, parses the SSE token deltas, and
// re-emits our own events via send. It splits the reply from the HTML on the fly
// using a hold-back window so a sentinel spanning two tokens still matches, then
// emits a final "done" with the canonical values from splitReplyAndHTML.
func (h *GenerateHandler) streamConverse(ctx context.Context, msgs []claudeMessage, currentHTML string, send func(map[string]any)) error {
	system := buildGenerateSystem(currentHTML)
	out := flattenToOpenAI(system, msgs)

	body, err := json.Marshal(openaiRequest{
		Model:     h.streamModel,
		MaxTokens: 8192,
		Messages:  out,
		Stream:    true,
	})
	if err != nil {
		return err
	}

	endpoint := strings.TrimRight(h.streamBaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("authorization", "Bearer "+h.streamAPIKey)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("%s stream status %d: %s", h.streamProvider, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var acc strings.Builder // full raw model text
	sentinelAt := -1        // index in acc where the sentinel starts (once found)
	emittedReply := 0       // bytes of reply already streamed
	emittedHTML := 0        // index in acc from which HTML is not yet streamed
	firstToken := false
	hold := len(siteHTMLSentinel) - 1 // reply bytes held back in case a sentinel is mid-arrival

	onDelta := func(d string) {
		if d == "" {
			return
		}
		if !firstToken {
			firstToken = true
			send(map[string]any{"type": "stage", "value": "generating"})
		}
		acc.WriteString(d)
		full := acc.String()

		if sentinelAt == -1 {
			if idx := strings.Index(full, siteHTMLSentinel); idx != -1 {
				if idx > emittedReply {
					send(map[string]any{"type": "reply", "delta": full[emittedReply:idx]})
				}
				emittedReply = idx
				sentinelAt = idx
				emittedHTML = idx + len(siteHTMLSentinel)
				send(map[string]any{"type": "stage", "value": "writing"})
				if len(full) > emittedHTML {
					send(map[string]any{"type": "html", "delta": full[emittedHTML:]})
					emittedHTML = len(full)
				}
				return
			}
			// Sentinel not seen yet: emit reply up to a rune boundary, holding
			// back the last few bytes that could be the start of the sentinel.
			safe := len(full) - hold
			for safe > emittedReply && !utf8.RuneStart(full[safe]) {
				safe--
			}
			if safe > emittedReply {
				send(map[string]any{"type": "reply", "delta": full[emittedReply:safe]})
				emittedReply = safe
			}
			return
		}
		// In the HTML body: emit whatever arrived (delta boundaries are rune-safe).
		if len(full) > emittedHTML {
			send(map[string]any{"type": "html", "delta": full[emittedHTML:]})
			emittedHTML = len(full)
		}
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, rerr := reader.ReadString('\n')
		if s := strings.TrimRight(line, "\r\n"); s != "" && strings.HasPrefix(s, "data:") {
			data := strings.TrimSpace(s[len("data:"):])
			if data == "[DONE]" {
				break
			}
			var chunk openaiStreamChunk
			if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
				onDelta(chunk.Choices[0].Delta.Content)
			}
		}
		if rerr != nil {
			break
		}
	}

	// Authoritative final values — the client replaces streamed UX with these.
	reply, html, _ := splitReplyAndHTML(acc.String())
	send(map[string]any{"type": "done", "reply": reply, "html": html})
	return nil
}
