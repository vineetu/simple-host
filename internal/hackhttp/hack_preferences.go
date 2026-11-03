package hackhttp

import (
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

func (h *HackHandler) registerHackPreferences(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/preferences", wrap(h.getHackPreferences))
	mux.Handle("PATCH /v1/hack/preferences", wrap(h.patchHackPreferences))
}

func (h *HackHandler) getHackPreferences(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	p, err := h.eventsSvc().HackPreferences(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hackPreferencesJSON(user, p))
}

// hackPreferencesJSON adds email_possible: false for a key-only account
// (a sign-in name that is not an email address), which is never emailed
// whatever email_updates says.
func hackPreferencesJSON(user *db.User, p model.HackPreferences) map[string]any {
	return map[string]any{
		"theme":                      p.Theme,
		"organiser_walkthrough_done": p.OrganiserWalkthroughDone,
		"judge_walkthrough_done":     p.JudgeWalkthroughDone,
		"email_updates":              p.EmailUpdates,
		"email_possible":             strings.Contains(user.Username, "@"),
	}
}

func (h *HackHandler) patchHackPreferences(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	var req struct {
		Theme                    *string `json:"theme"`
		OrganiserWalkthroughDone *bool   `json:"organiser_walkthrough_done"`
		JudgeWalkthroughDone     *bool   `json:"judge_walkthrough_done"`
		EmailUpdates             *bool   `json:"email_updates"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	p, err := h.eventsSvc().HackPreferences(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if req.Theme != nil {
		if *req.Theme != "system" && *req.Theme != "light" && *req.Theme != "dark" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_theme", "theme must be system, light or dark")
			return
		}
		p.Theme = *req.Theme
	}
	if req.OrganiserWalkthroughDone != nil {
		p.OrganiserWalkthroughDone = *req.OrganiserWalkthroughDone
	}
	if req.JudgeWalkthroughDone != nil {
		p.JudgeWalkthroughDone = *req.JudgeWalkthroughDone
	}
	if req.EmailUpdates != nil {
		p.EmailUpdates = *req.EmailUpdates
	}
	if err := h.eventsSvc().SetHackPreferencesLookup(r.Context(), user.ID, p); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hackPreferencesJSON(user, p))
}
