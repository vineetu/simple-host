package notices

import (
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) getHackPreferences(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	p, err := h.Events.HackPreferences(r.Context(), user.ID)
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

func (h *handler) patchHackPreferences(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	var req struct {
		Theme                    *string `json:"theme"`
		OrganiserWalkthroughDone *bool   `json:"organiser_walkthrough_done"`
		JudgeWalkthroughDone     *bool   `json:"judge_walkthrough_done"`
		EmailUpdates             *bool   `json:"email_updates"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	p, err := h.Events.PatchPreferences(r.Context(), user.ID, events.PreferencesPatch(req))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hackPreferencesJSON(user, p))
}
