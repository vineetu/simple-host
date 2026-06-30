package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
)

// GenerateHandler powers the home page "create with AI" box: a signed-in user
// describes a site in plain English and Claude (Haiku by default) returns a
// single self-contained HTML file to preview and then deploy.
//
// It is deliberately sign-in-gated (mounted behind the auth middleware) and
// rate limited per user and per IP, because each call spends real Anthropic
// credits. Disabled entirely when ANTHROPIC_API_KEY is unset.
type GenerateHandler struct {
	apiKey      string
	model       string
	client      *http.Client
	ipLimiter   *rateLimiter
	userLimiter *rateLimiter
}

func NewGenerateHandler(apiKey, model string) *GenerateHandler {
	// Bounded but generous: a small burst, then a slow refill. Cost guard, not
	// a UX obstacle for a real person iterating on one site.
	ipLimiter := newRateLimiter(8, 1.0/30.0)   // ~burst 8, +1 every 30s
	userLimiter := newRateLimiter(12, 1.0/20.0) // ~burst 12, +1 every 20s
	ipLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	userLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	return &GenerateHandler{
		apiKey:      apiKey,
		model:       model,
		client:      &http.Client{Timeout: 90 * time.Second},
		ipLimiter:   ipLimiter,
		userLimiter: userLimiter,
	}
}

func (h *GenerateHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/generate", authMW(http.HandlerFunc(h.generate)))
}

type generateRequest struct {
	Prompt string `json:"prompt"`
}

func (h *GenerateHandler) generate(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "sign in to use AI create"})
		return
	}
	if !h.ipLimiter.allow(clientIP(r)) || !h.userLimiter.allow(user.ID) {
		tooManyRequests(w)
		return
	}

	var req generateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if len(req.Prompt) < 3 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "describe what you want to build"})
		return
	}
	if len(req.Prompt) > 2000 {
		req.Prompt = req.Prompt[:2000]
	}

	html, err := h.callClaude(r.Context(), req.Prompt)
	if err != nil {
		log.Printf("generate: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "generation failed — please try again"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": html})
}

const generateSystemPrompt = `You are a senior web designer. The user describes a website; you return ONE complete, self-contained HTML document and NOTHING else.

Hard rules:
- Output ONLY raw HTML starting with <!DOCTYPE html>. No markdown, no code fences, no commentary before or after.
- Everything inline in the single file: CSS in a <style> tag, any JS in a <script> tag. No external build steps.
- The only external resources you may reference are Google Fonts (fonts.googleapis.com) and, if the page benefits from a live discussion section, the simple-host comments widget: <script src="https://simple-host.app/comments.js" defer></script> with a <section id="sh-comments"></section> where it should appear.
- Use a SOLID page background (never a gradient on <body> — it breaks the widgets). Gradients are fine on hero/section blocks.
- Modern, clean, responsive, accessible. Good type scale, spacing, and contrast. Fill in realistic, specific placeholder content that fits the request (no lorem ipsum, no leftover template brand names).
- Keep it to a single page. Aim for tasteful, not bloated.`

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (h *GenerateHandler) callClaude(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(claudeRequest{
		Model:     h.model,
		MaxTokens: 8192,
		System:    generateSystemPrompt,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", h.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}

	var parsed claudeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil {
		// Don't leak upstream specifics to the client; log is enough.
		log.Printf("generate: anthropic error: %s", parsed.Error.Message)
		return "", io.EOF
	}

	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return cleanHTML(sb.String()), nil
}

// cleanHTML strips accidental markdown fences and leading prose so the result is
// a clean HTML document even if the model wraps it.
func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i != -1 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	// If the model added a preamble, snap to the first doctype/html tag.
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "<!doctype"); i > 0 {
		s = s[i:]
	} else if i := strings.Index(lower, "<html"); i > 0 {
		s = s[i:]
	}
	s = strings.TrimSpace(s)
	// Guarantee a doctype so browsers render in standards mode (Haiku sometimes
	// omits it).
	if !strings.HasPrefix(strings.ToLower(s), "<!doctype") && strings.HasPrefix(strings.ToLower(s), "<html") {
		s = "<!DOCTYPE html>\n" + s
	}
	return s
}
