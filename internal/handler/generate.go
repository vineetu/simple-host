package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
)

// GenerateHandler powers the home page "create with AI" chat: a signed-in user
// has a short planning conversation with Claude (Haiku by default), which asks
// clarifying questions, proposes a plan, and returns a single self-contained
// HTML file to preview, refine, and then deploy.
//
// Sign-in-gated (mounted behind the auth middleware) and rate limited per user
// and per IP, because each turn spends real Anthropic credits. Disabled when
// ANTHROPIC_API_KEY is unset.
type GenerateHandler struct {
	apiKey      string
	model       string
	client      *http.Client
	ipLimiter   *rateLimiter
	userLimiter *rateLimiter
}

func NewGenerateHandler(apiKey, model string) *GenerateHandler {
	// A conversation is several turns, so allow a healthy burst; the slow refill
	// is the real cost guard against scripted abuse.
	ipLimiter := newRateLimiter(20, 1.0/12.0)   // burst 20, +1 every 12s
	userLimiter := newRateLimiter(30, 1.0/10.0) // burst 30, +1 every 10s
	ipLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	userLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	return &GenerateHandler{
		apiKey:      apiKey,
		model:       model,
		client:      &http.Client{Timeout: 120 * time.Second},
		ipLimiter:   ipLimiter,
		userLimiter: userLimiter,
	}
}

func (h *GenerateHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/generate", authMW(http.HandlerFunc(h.generate)))
}

type generateRequest struct {
	Messages []claudeMessage `json:"messages"`
	// HTML is the current version of the site (if any), passed back so the model
	// can make incremental edits without the whole document living in the chat
	// transcript.
	HTML string `json:"html"`
	// Attachments ride along with the latest user message: images (vision),
	// PDFs (document blocks), or text files (inlined into the prompt).
	Attachments []attachmentIn `json:"attachments"`
}

// attachmentIn is one user-supplied file from the chat. Images/PDFs carry base64
// Data; text files carry plain Text.
type attachmentIn struct {
	Kind      string `json:"kind"`      // "image" | "document" | "text"
	MediaType string `json:"mediaType"` // for image/document
	Name      string `json:"name"`
	Data      string `json:"data"` // base64 (image/document)
	Text      string `json:"text"` // text files
}

type generateResponse struct {
	Reply string `json:"reply"`
	HTML  string `json:"html,omitempty"`
}

const (
	// Keep plenty of turns so a user can iterate on one site for a long session
	// (the user asked for 25+ refinement turns). ~60 messages ≈ 30 exchanges; on
	// top of that the current HTML is always re-sent, so edits keep working even
	// once the oldest chat turns scroll out of the window.
	maxMessages      = 60
	maxMessageChars  = 6000
	maxCurrentHTML   = 200 * 1024
	siteHTMLSentinel = "<<<SITE_HTML>>>"
)

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
	// Large cap because attachments (images/PDFs) ride in the JSON as base64.
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
		li := len(msgs) - 1 // attach to the latest user turn
		msgs[li].Content = buildUserBlocks(msgs[li].Content, atts)
	}

	reply, html, err := h.converse(r.Context(), msgs, req.HTML)
	if err != nil {
		log.Printf("generate: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "the assistant had trouble — please try again"})
		return
	}
	writeJSON(w, http.StatusOK, generateResponse{Reply: reply, HTML: html})
}

// sanitizeMessages trims, caps, and validates the conversation, keeping only the
// most recent turns with sane roles.
func sanitizeMessages(in []claudeMessage) []claudeMessage {
	out := make([]claudeMessage, 0, len(in))
	for _, m := range in {
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		s, _ := m.Content.(string) // incoming messages are plain strings
		c := strings.TrimSpace(s)
		if c == "" {
			continue
		}
		if len(c) > maxMessageChars {
			c = c[:maxMessageChars]
		}
		out = append(out, claudeMessage{Role: role, Content: c})
	}
	if len(out) > maxMessages {
		out = out[len(out)-maxMessages:]
	}
	// Anthropic requires the first message to be from the user.
	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out
}

const (
	maxAttachments      = 6
	maxAttachTextChars  = 100_000
	maxAttachTotalBytes = 18 << 20
)

var allowedImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// sanitizeAttachments validates user-supplied files (type allowlist, per-file and
// total size caps, count cap). The data itself is opaque base64 we pass straight
// to Anthropic — it never touches our disk or shell.
func sanitizeAttachments(in []attachmentIn) ([]attachmentIn, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxAttachments {
		return nil, fmt.Errorf("too many attachments (max %d)", maxAttachments)
	}
	out := make([]attachmentIn, 0, len(in))
	total := 0
	for _, a := range in {
		switch a.Kind {
		case "image":
			if !allowedImageTypes[a.MediaType] {
				return nil, fmt.Errorf("unsupported image type %q", a.MediaType)
			}
			if a.Data == "" {
				return nil, fmt.Errorf("empty image data")
			}
			if len(a.Data) > 7<<20 { // ~5 MB binary after base64
				return nil, fmt.Errorf("image %q is too large (5 MB max)", a.Name)
			}
			total += len(a.Data)
			out = append(out, attachmentIn{Kind: "image", MediaType: a.MediaType, Name: a.Name, Data: a.Data})
		case "document":
			if a.MediaType != "application/pdf" {
				return nil, fmt.Errorf("only PDF documents are supported")
			}
			if a.Data == "" {
				return nil, fmt.Errorf("empty document")
			}
			if len(a.Data) > 24<<20 {
				return nil, fmt.Errorf("PDF %q is too large", a.Name)
			}
			total += len(a.Data)
			out = append(out, attachmentIn{Kind: "document", MediaType: "application/pdf", Name: a.Name, Data: a.Data})
		case "text":
			t := a.Text
			if len(t) > maxAttachTextChars {
				t = t[:maxAttachTextChars]
			}
			total += len(t)
			out = append(out, attachmentIn{Kind: "text", Name: a.Name, Text: t})
		default:
			return nil, fmt.Errorf("unsupported attachment kind %q", a.Kind)
		}
		if total > maxAttachTotalBytes {
			return nil, fmt.Errorf("attachments are too large in total")
		}
	}
	return out, nil
}

// buildUserBlocks turns the latest user message into a content-block array: the
// image/document blocks first, then a single text block (the typed message plus
// any inlined text files). Anthropic requires a non-empty text block.
func buildUserBlocks(textContent any, atts []attachmentIn) []any {
	text, _ := textContent.(string)
	var blocks []any
	var extra strings.Builder
	for _, a := range atts {
		switch a.Kind {
		case "image":
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "base64", "media_type": a.MediaType, "data": a.Data},
			})
		case "document":
			blocks = append(blocks, map[string]any{
				"type":   "document",
				"source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": a.Data},
			})
		case "text":
			extra.WriteString("\n\n--- Attached file: " + a.Name + " ---\n" + a.Text)
		}
	}
	t := strings.TrimSpace(text + extra.String())
	if t == "" {
		t = "Use the attached file(s) to build the site."
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": t})
	return blocks
}

const generateSystemPrompt = `You are a warm, sharp web-design assistant inside the simple-host site builder. You help a non-technical person create ONE single-page website through a short, friendly conversation.

How to behave:
- If the request is vague, ask AT MOST 1-2 short clarifying questions (e.g. name? overall vibe? what should it do?). Don't interrogate — as soon as you have enough, build it.
- When you have enough to build, or the user asks for the site or a change, produce the site.
- Keep chat replies to a sentence or two, friendly and concrete. NEVER paste HTML or code into the chat text.
- The user may attach images or files. Use them: treat an image as visual reference — replicate a mockup's layout/colors/typography, read text or data out of a screenshot, or match branding; treat attached text/PDF files as content or source to incorporate. (You receive attachments to look at; you can't embed the raw uploaded bytes, so recreate the look or use the content.)

OUTPUT FORMAT — follow exactly:
- First write your short conversational reply as plain text.
- THEN, ONLY when you are creating or updating the site, output on its own line the exact marker ` + siteHTMLSentinel + ` followed immediately by the COMPLETE HTML document.
- If you are only asking a question or chatting, do NOT output the marker or any HTML.

The HTML document:
- One complete self-contained file starting with <!DOCTYPE html>. All CSS in a <style> tag, all JS in a <script> tag.
- Only external resources allowed: Google Fonts, and optionally the simple-host comments widget (<script src="https://simple-host.app/comments.js" defer></script> with <section id="sh-comments"></section>).
- Distinctive, production-grade design — NOT generic AI slop. Commit to a clear aesthetic that fits the brief (editorial, brutalist, warm/organic, refined-luxury, playful, retro, etc.). Use beautiful, characterful typography (never Arial/Inter/system defaults), a strong type scale, intentional color with sharp accents, tasteful motion (e.g. a staggered page-load reveal), generous spacing, and strong contrast. Responsive and accessible. Use a SOLID page background (gradients only on hero/section blocks, never on <body>).
- Fill in realistic, specific content for the brief — no lorem ipsum, no leftover template brand names.
- When updating an existing site, return the FULL revised document, keeping everything except the requested change.`

func (h *GenerateHandler) converse(ctx context.Context, msgs []claudeMessage, currentHTML string) (string, string, error) {
	system := generateSystemPrompt
	if strings.TrimSpace(currentHTML) != "" {
		if len(currentHTML) > maxCurrentHTML {
			currentHTML = currentHTML[:maxCurrentHTML]
		}
		system += "\n\nThe current version of the site is below. When the user asks for a change, return the FULL revised document.\n<<<CURRENT_SITE>>>\n" + currentHTML
	}

	body, err := json.Marshal(claudeRequest{
		Model:     h.model,
		MaxTokens: 8192,
		System:    system,
		Messages:  msgs,
	})
	if err != nil {
		return "", "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("x-api-key", h.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}

	var parsed claudeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", err
	}
	if parsed.Error != nil {
		log.Printf("generate: anthropic error: %s", parsed.Error.Message)
		return "", "", io.EOF
	}

	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return splitReplyAndHTML(sb.String())
}

// splitReplyAndHTML separates the conversational reply from the optional HTML
// document, which the model delimits with the sentinel marker.
func splitReplyAndHTML(text string) (string, string, error) {
	if i := strings.Index(text, siteHTMLSentinel); i != -1 {
		reply := strings.TrimSpace(text[:i])
		html := cleanHTML(text[i+len(siteHTMLSentinel):])
		if reply == "" {
			reply = "Here's your site — take a look on the right."
		}
		return reply, html, nil
	}
	return strings.TrimSpace(text), "", nil
}

type claudeMessage struct {
	Role string `json:"role"`
	// Content is a plain string for normal turns, or a []any of content blocks
	// (image/document/text) for a user turn that carries attachments.
	Content any `json:"content"`
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

// cleanHTML strips accidental markdown fences/preamble and guarantees a doctype
// so browsers render in standards mode.
func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i != -1 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "<!doctype"); i > 0 {
		s = s[i:]
	} else if i := strings.Index(lower, "<html"); i > 0 {
		s = s[i:]
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(s), "<!doctype") && strings.HasPrefix(strings.ToLower(s), "<html") {
		s = "<!DOCTYPE html>\n" + s
	}
	return s
}
