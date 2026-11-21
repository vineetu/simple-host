package hackhttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/members"
)

// previewLeaveEvent is GET .../leave: read-only, every case, never errors for a member beyond 404.
func (h *participationHTTP) previewLeaveEvent(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	preview, err := h.members.PreviewLeave(r.Context(), eventSlug(r), user.ID)
	if preview.Event.ID != "" {
		paperHackEvent(r, preview.Event)
	}
	if err != nil {
		writeHackError(w, err)
		return
	}
	paperHackKnownMember(r, preview.Event, user.ID)
	// The creator case carries other organisers' login emails; X-API-Key auth means a cache is not required to skip storing the GET, so say so explicitly.
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, preview)
}

// postLeaveEvent is POST .../leave: the caller leaves the event themselves.
func (h *participationHTTP) postLeaveEvent(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil || h.codes.limited(w, r, user.ID) {
		return
	}
	var req struct {
		Confirm         string `json:"confirm"`
		SuccessorUserID string `json:"successor_user_id"`
		Close           string `json:"close"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	result, err := h.members.Leave(r.Context(), eventSlug(r), user.ID, members.LeaveRequest{Confirm: req.Confirm, SuccessorID: req.SuccessorUserID, Close: req.Close}, h.links.trash())
	if result.Event.ID != "" {
		paperHackEvent(r, result.Event)
	}
	if err != nil {
		writeHackError(w, err)
		return
	}
	switch {
	case result.Outcome == "ended":
		paperHackStage(r, result.Event)
		paperHackTimes(r)
		h.links.pinDue(r.Context(), result.Event.ID)
	case result.Outcome == "deleted":
		h.links.removeEventFiles(result.Event)
	case result.Case == "participant":
		h.links.removeOrphanTeamSites(r.Context(), result.Event)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, result)
}

func eventSlug(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
}
