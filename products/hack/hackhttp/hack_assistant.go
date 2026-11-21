package hackhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

// assistantSecretKeyWords: any event-view JSON key whose lowercased name
// contains one of these is dropped before the view reaches the prompt.
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
func (h *assistantHTTP) sanitizedAssistantEventView(ctx context.Context, ev model.Event, member model.EventMember) (json.RawMessage, error) {
	view := h.views.eventView(ctx, ev, member, true)
	// entry_required is the legacy six-field list (kept one release for old
	// clients): it cannot say "One of" and has no website, so next to CURRENT
	// SETTINGS it would give the model a second, wrong answer to "what do teams
	// hand in?".
	if inner, ok := view["event"].(map[string]any); ok {
		delete(inner, "entry_required")
		delete(inner, "results_visibility")
	}
	return sanitizeAssistantEventViewJSON(view)
}

// sanitizeAssistantEventViewJSON does the marshal/unmarshal-to-`any`/
// sanitise/marshal round trip described above.
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

// assistantAccess loads the caller's membership and enforces organisers only:
// every other role, and a platform admin inspecting an event it has not joined
// (loadMember's admin bypass), get 403 organisers_only.
func (h *assistantHTTP) assistantAccess(w http.ResponseWriter, r *http.Request) (hackAccess, bool) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false)
	if !ok {
		return hackAccess{}, false
	}
	if a.admin || a.member.Role != "organiser" {
		httpx.WriteError(w, http.StatusForbidden, "organisers_only", "only organisers can ask the assistant")
		return hackAccess{}, false
	}
	// loadMember only checks TakenDown for write=true, and the assistant
	// (read-only, no tools) doesn't otherwise need that -- but asking it questions
	// about a taken-down event is its own kind of "write" here: the event is in a
	// state the organiser should not be getting guidance about.
	if a.event.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return hackAccess{}, false
	}
	return a, true
}

func (h *assistantHTTP) getAssistant(w http.ResponseWriter, r *http.Request) {
	a, ok := h.assistantAccess(w, r)
	if !ok {
		return
	}
	if h.service == nil || !h.service.Enabled() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	status, err := h.service.Status(r.Context(), a.user.ID)
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

// Organiser assistant v2: optional "screen" (navigation screen id) and "tab"
// say where the organiser is asking from.
type assistantRequest struct {
	Messages []events.ChatMessage `json:"messages"`
	Screen   string               `json:"screen"`
	Tab      string               `json:"tab"`
}

func decodeAssistantMessages(w http.ResponseWriter, r *http.Request) (assistantRequest, bool) {
	var req assistantRequest
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	body := map[string]any{"error": refusal.Message, "code": refusal.Code}
	switch refusal.Code {
	case "daily_limit":
		body["remaining_today"] = refusal.RemainingToday
	case "monthly_budget":
		body["resets"] = refusal.Resets
	}
	return body
}

func (h *assistantHTTP) postAssistant(w http.ResponseWriter, r *http.Request) {
	a, ok := h.assistantAccess(w, r)
	if !ok {
		return
	}
	if h.service == nil || !h.service.Enabled() {
		httpx.WriteError(w, http.StatusNotFound, "assistant_off", "The assistant is not set up on this server.")
		return
	}
	if !h.writes.Allow(a.user.ID) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many questions at once. Wait a minute and try again.")
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
	in := events.AskContext{
		Slug:      a.event.Slug,
		CanJudge:  h.events.MemberAbilities(r.Context(), a.event.ID, a.member).Judge,
		EventView: eventView,
		Settings:  h.assistantCurrentSettings(r.Context(), a.event),
		Screen:    req.Screen,
		Tab:       req.Tab,
	}
	stream := newSSEStream(w)
	result, err := h.service.Ask(r.Context(), a.user.ID, in, req.Messages, func(delta string) {
		stream.send(map[string]any{"delta": delta})
	})
	if err != nil {
		stream.fail(err)
		return
	}
	stream.send(map[string]any{"done": true, "remaining_today": result.RemainingToday})
}

// sseStream is the assistant's answer as server-sent events.
type sseStream struct {
	w http.ResponseWriter
	// rc, not a w.(http.Flusher) assertion: in production w arrives wrapped
	// (apiMetrics.Wrap's statusCapture, at least), and a wrapper's embedded
	// ResponseWriter does not promote Flush -- only its Unwrap does, which is what
	// ResponseController follows.
	rc      *http.ResponseController
	started bool
}

func newSSEStream(w http.ResponseWriter) *sseStream {
	return &sseStream{w: w, rc: http.NewResponseController(w)}
}

// send writes one event, starting the stream first if needed.
func (s *sseStream) send(v any) {
	if !s.started {
		s.w.Header().Set("Content-Type", "text/event-stream")
		s.w.Header().Set("Cache-Control", "no-store")
		s.w.Header().Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	_ = s.rc.Flush()
}

// fail answers an Ask error: before the stream starts, a refusal is its JSON
// answer and anything else a plain 500; once started, the stream ends with the
// contract's one error event.
func (s *sseStream) fail(err error) {
	var refusal *events.AssistantRefusal
	isRefusal := errors.As(err, &refusal)
	switch {
	case !s.started && isRefusal:
		httpx.WriteJSON(s.w, assistantRefusalStatus(refusal.Code), assistantRefusalBody(refusal))
	case !s.started:
		httpx.WriteInternal(s.w)
	case isRefusal:
		s.send(map[string]any{"error": refusal.Code, "message": refusal.Message})
	default:
		s.send(map[string]any{"error": "provider_error", "message": "The assistant couldn't answer. Try again."})
	}
}
