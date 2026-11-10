package hackhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// registerAssistant wires SIM-394's organiser assistant chat. The API
// contract (fixed by the lead) is exact about the JSON shape of every
// refusal -- the top-level "error" field holds the stable code (not a
// human message, unlike httpx.ErrorResponse elsewhere in this package) --
// so every response here is built by hand rather than through
// httpx.WriteError.
func (h *HackHandler) registerAssistant(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/assistant", wrap(h.getAssistant))
	mux.Handle("POST /v1/hack/events/{slug}/assistant", wrap(h.postAssistant))
}

// assistantSecretKeyWords: any event-view JSON key whose lowercased name
// contains one of these is dropped before the view reaches the prompt. The
// contract (api-contract.md) lists the first seven; "url" is added here
// because join_url/judge_url (hack.go's joinURL/judgeURL) embed the exact
// same join_code/judge_code value the first seven already strip bare --
// dropping the code but keeping the URL it is embedded in would leak it
// right back out, handing the model (and so a careless answer) a working
// unsupervised join/judge link. Every other "*url*" field this view
// carries (icon_url, the event's own public url/builtin_url) is a public
// address anyway, and none of them render as a clickable link in the
// browser regardless (only /e/{slug}/... and /docs/... hrefs do), so
// dropping them all costs nothing the assistant actually needs.
var assistantSecretKeyWords = []string{"code", "token", "key", "secret", "password", "email", "link", "url"}

// sanitizeAssistantEventView recursively drops any map key containing a
// word in assistantSecretKeyWords, plus "admin_view", at any depth.
func sanitizeAssistantEventView(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			lk := strings.ToLower(k)
			if lk == "admin_view" {
				continue
			}
			drop := false
			for _, bad := range assistantSecretKeyWords {
				if strings.Contains(lk, bad) {
					drop = true
					break
				}
			}
			if drop {
				continue
			}
			out[k] = sanitizeAssistantEventView(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitizeAssistantEventView(val)
		}
		return out
	default:
		return v
	}
}

// sanitizedAssistantEventView builds the GET /v1/hack/events/{slug} body
// (organiser view, so it carries "checklist" and "can") and strips every
// secret-looking field before it is ever handed to the model.
//
// eventView's map[string]any carries whatever Go value each field builder
// happened to produce -- a nested struct, a []model.Something, a
// map[string]int -- not necessarily a generic map[string]any/[]any.
// sanitizeAssistantEventView's recursion only matches those two generic
// shapes, so it would silently pass through, say, a []map[string]any of
// conflict rows or a struct with its own "join_code" field untouched.
// Marshalling to JSON and back into `any` first canonicalises every
// nested value into exactly the shapes the sanitiser walks (object,
// array, string, number, bool, null), so nothing with a secret-looking
// field name can hide behind a concrete Go type.
func (h *HackHandler) sanitizedAssistantEventView(ctx context.Context, ev model.Event, member model.EventMember) (json.RawMessage, error) {
	view := h.eventView(ctx, ev, member, true)
	// entry_required is the legacy six-field list (kept one release for
	// old clients): it cannot say "One of" and has no website, so next to
	// CURRENT SETTINGS it would give the model a second, wrong answer to
	// "what do teams hand in?". Dropped from the assistant's copy only.
	// results_visibility is the legacy column; the published results row's
	// full_ranking is what the public page and Settings > Results use, and
	// CURRENT SETTINGS carries that.
	if inner, ok := view["event"].(map[string]any); ok {
		delete(inner, "entry_required")
		delete(inner, "results_visibility")
	}
	return sanitizeAssistantEventViewJSON(view)
}

// sanitizeAssistantEventViewJSON does the marshal/unmarshal-to-`any`/
// sanitise/marshal round trip described above. Factored out (rather than
// inlined in sanitizedAssistantEventView) so it is directly testable with
// any Go value -- a struct, a []model.Something, a map[string]int -- not
// only the map[string]any/[]any shapes sanitizeAssistantEventView itself
// walks.
func sanitizeAssistantEventViewJSON(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return json.Marshal(sanitizeAssistantEventView(generic))
}

// assistantAccess loads the caller's membership and enforces organisers
// only: every other role, and a platform admin inspecting an event it has
// not joined (loadMember's admin bypass), get 403 organisers_only. Unlike
// loadMember's own roles filter (which answers a wrong role with 404
// event_not_found), the contract is explicit that this route always
// answers 403 here, so the check is done by hand instead of passed to
// loadMember as a roles filter.
func (h *HackHandler) assistantAccess(w http.ResponseWriter, r *http.Request) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return hackAccess{}, false
	}
	if a.admin || a.member.Role != "organiser" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "organisers_only"})
		return hackAccess{}, false
	}
	// loadMember only checks TakenDown for write=true, and the assistant
	// (read-only, no tools) doesn't otherwise need that -- but asking it
	// questions about a taken-down event is its own kind of "write" here:
	// the event is in a state the organiser should not be getting
	// guidance about. A normally-ended (archived) event is a different,
	// unflagged state and keeps asking.
	if a.event.TakenDown() {
		// This route's "error" field is always the stable code (see the
		// package doc comment above), so the hand-rolled shape, not
		// httpx.WriteError (whose "error" field carries the message and
		// puts the code in "code" instead).
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "event_taken_down", "message": "this event has been taken down"})
		return hackAccess{}, false
	}
	return a, true
}

func (h *HackHandler) getAssistant(w http.ResponseWriter, r *http.Request) {
	a, ok := h.assistantAccess(w, r)
	if !ok {
		return
	}
	if h.assistant == nil || !h.assistant.Enabled() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	status, err := h.assistant.Status(r.Context(), a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	body := map[string]any{
		"enabled":         status.Enabled,
		"daily_limit":     status.DailyLimit,
		"used_today":      status.UsedToday,
		"remaining_today": status.RemainingToday,
		"budget_reached":  status.BudgetReached,
	}
	if status.Resets != "" {
		body["resets"] = status.Resets
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// decodeAssistantMessages decodes {"messages":[...]}; a malformed or
// oversized body is the contract's 400 bad_request shape.
//
// Organiser assistant v2: optional "screen" (navigation screen id) and
// "tab" say where the organiser is asking from. They only word a line of
// the prompt; anything the navigation map doesn't know is ignored, and
// each is capped so a client can't stuff the prompt through them.
type assistantRequest struct {
	Messages []events.ChatMessage `json:"messages"`
	Screen   string               `json:"screen"`
	Tab      string               `json:"tab"`
}

func decodeAssistantMessages(w http.ResponseWriter, r *http.Request) (assistantRequest, bool) {
	var req assistantRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, hackMaxBody))
	if err := dec.Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_request", "message": "invalid JSON body"})
		return assistantRequest{}, false
	}
	if len(req.Screen) > 64 {
		req.Screen = ""
	}
	if len(req.Tab) > 64 {
		req.Tab = ""
	}
	return req, true
}

// assistantRefusalStatus maps an *events.AssistantRefusal's Code to the
// contract's HTTP status for a pre-stream refusal.
func assistantRefusalStatus(code string) int {
	switch code {
	case "daily_limit", "monthly_budget":
		return http.StatusTooManyRequests
	case "assistant_off":
		return http.StatusNotFound
	case "provider_error":
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func assistantRefusalBody(refusal *events.AssistantRefusal) map[string]any {
	body := map[string]any{"error": refusal.Code, "message": refusal.Message}
	switch refusal.Code {
	case "daily_limit":
		body["remaining_today"] = refusal.RemainingToday
	case "monthly_budget":
		body["resets"] = refusal.Resets
	}
	return body
}

func (h *HackHandler) postAssistant(w http.ResponseWriter, r *http.Request) {
	a, ok := h.assistantAccess(w, r)
	if !ok {
		return
	}
	if h.assistant == nil || !h.assistant.Enabled() {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "assistant_off", "message": "The assistant is not set up on this server."})
		return
	}
	if !h.assistantWrites.Allow(a.user.ID) {
		// This route's refusals are the contract's hand-rolled
		// {"error","message"} shape throughout (see the package doc
		// comment above), not httpx.ErrorResponse's {"error","code"} --
		// so the per-account rate limit matches that shape here too,
		// rather than reusing httpx.ErrorResponse's generic wording.
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "message": "Too many questions at once. Wait a minute and try again."})
		return
	}
	req, ok := decodeAssistantMessages(w, r)
	if !ok {
		return
	}

	eventView, err := h.sanitizedAssistantEventView(r.Context(), a.event, a.member)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	canJudge := h.eventsSvc().MemberAbilities(r.Context(), a.event.ID, a.member).Judge

	// http.NewResponseController, not a w.(http.Flusher) type assertion:
	// in production w is never the bare ResponseWriter the server handed
	// the mux. It arrives wrapped (apiMetrics.Wrap's statusCapture, at
	// least), and a wrapper's embedded http.ResponseWriter field does not
	// promote the concrete writer's Flush method -- only its own Unwrap
	// does, which is exactly what ResponseController follows. Without
	// this, every delta sits buffered until the whole answer is in and
	// the handler returns, defeating the stream entirely.
	rc := http.NewResponseController(w)
	headerSent := false
	sendHeaders := func() {
		if headerSent {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		headerSent = true
	}
	writeSSE := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		_ = rc.Flush()
	}

	in := events.AskContext{
		Slug:      a.event.Slug,
		CanJudge:  canJudge,
		EventView: eventView,
		Settings:  h.assistantCurrentSettings(r.Context(), a.event),
		Screen:    req.Screen,
		Tab:       req.Tab,
	}
	result, askErr := h.assistant.Ask(r.Context(), a.user.ID, in, req.Messages, func(delta string) {
		sendHeaders()
		writeSSE(map[string]any{"delta": delta})
	})

	if askErr != nil {
		var refusal *events.AssistantRefusal
		if !errors.As(askErr, &refusal) {
			// A genuine infrastructure failure (e.g. the database): if we
			// have not committed to the stream yet, a plain 500; if we
			// have, there is no JSON-body way left to say so, so end the
			// stream with the one error shape the contract defines.
			if !headerSent {
				httpx.WriteInternal(w)
				return
			}
			writeSSE(map[string]any{"error": "provider_error", "message": "The assistant couldn't answer. Try again."})
			return
		}
		if !headerSent {
			httpx.WriteJSON(w, assistantRefusalStatus(refusal.Code), assistantRefusalBody(refusal))
			return
		}
		// Only provider_error can happen after streaming has already
		// started (daily_limit/monthly_budget/bad_request/assistant_off are
		// always decided before the first delta).
		writeSSE(map[string]any{"error": refusal.Code, "message": refusal.Message})
		return
	}

	sendHeaders()
	writeSSE(map[string]any{"done": true, "remaining_today": result.RemainingToday})
}
