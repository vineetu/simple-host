package handler

import (
	"net/http"

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
	p, err := db.GetHackPreferences(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, p)
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
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	p, err := db.GetHackPreferences(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if req.Theme != nil {
		if *req.Theme != "system" && *req.Theme != "light" && *req.Theme != "dark" {
			writeHackErr(w, http.StatusBadRequest, "invalid_theme", "theme must be system, light or dark")
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
	if err := db.SetHackPreferences(r.Context(), h.database, user.ID, p); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
