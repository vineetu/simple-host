package hackhttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *HackHandler) getHackPreferences(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	p, err := h.events.HackPreferences(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hackPreferencesJSON(user, p))
}

// hackPreferencesJSON adds email_possible: false for a key-only account
// (a sign-in name that is not an email address), which is never emailed
// whatever email_updates says.
func hackPreferencesJSON(user *model.User, p hackmodel.HackPreferences) map[string]any {
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	p, err := h.events.PatchPreferences(r.Context(), user.ID, events.PreferencesPatch(req))
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hackPreferencesJSON(user, p))
}
