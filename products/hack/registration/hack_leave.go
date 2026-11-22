package registration

import (
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
)

// previewLeaveEvent is GET .../leave: read-only, every case, never errors for a member beyond 404.
func (h *handler) previewLeaveEvent(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	preview, err := h.PreviewLeave(r.Context(), eventSlug(r), user.ID)
	if preview.Event.ID != "" {
		core.PaperEvent(r, preview.Event)
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	core.PaperMember(r, preview.Event, user.ID)
	// The creator case carries other organisers' login emails; X-API-Key auth means a cache is not required to skip storing the GET, so say so explicitly.
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, preview)
}

// postLeaveEvent is POST .../leave: the caller leaves the event themselves.
func (h *handler) postLeaveEvent(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil || h.Codes.Limited(w, r, user.ID) {
		return
	}
	var req struct {
		Confirm         string `json:"confirm"`
		SuccessorUserID string `json:"successor_user_id"`
		Close           string `json:"close"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Close != "" && req.Close != "end" && req.Close != "delete" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "close must be end or delete")
		return
	}
	if req.SuccessorUserID != "" && !sites.UUIDShape.MatchString(req.SuccessorUserID) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "successor_user_id must be an id")
		return
	}
	result, err := h.Leave(r.Context(), eventSlug(r), user.ID, LeaveRequest{Confirm: req.Confirm, SuccessorID: req.SuccessorUserID, Close: req.Close}, h.Links.Trash())
	if result.Event.ID != "" {
		core.PaperEvent(r, result.Event)
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	switch {
	case result.Outcome == "ended":
		core.PaperStage(r, result.Event)
		core.PaperTimes(r)
		h.Links.PinDue(r.Context(), result.Event.ID)
	case result.Outcome == "deleted":
		h.Links.RemoveEventFiles(result.Event)
	case result.Case == "participant":
		h.Links.RemoveOrphanTeamSites(r.Context(), result.Event)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, result)
}

func eventSlug(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
}
