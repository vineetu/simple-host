package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// patchEventReq is PATCH /v1/hack/events/{slug}: absent (null) fields keep
// their value. Its fields are events.EventPatch's, in the same order.
type patchEventReq struct {
	AccentColor          *string   `json:"accent_color"`
	Theme                *string   `json:"theme"`
	Title                *string   `json:"title"`
	Tagline              *string   `json:"tagline"`
	About                *string   `json:"about"`
	Rules                *string   `json:"rules"`
	Prizes               *string   `json:"prizes"`
	CocText              *string   `json:"coc_text"`
	TimeZone             *string   `json:"time_zone"`
	StartsAt             *string   `json:"starts_at"`
	EndsAt               *string   `json:"ends_at"`
	TeamFinding          *bool     `json:"team_finding"`
	ApplicationsOpen     *bool     `json:"applications_open"`
	TeamSizeMax          *int      `json:"team_size_max"`
	OrganiserName        *string   `json:"organiser_name"`
	Organisation         *string   `json:"organisation"`
	ContactEmail         *string   `json:"contact_email"`
	Purpose              *string   `json:"purpose"`
	ExpectedParticipants *int      `json:"expected_participants"`
	SubmissionDeadline   *string   `json:"submission_deadline"`
	EntryRequired        *[]string `json:"entry_required"`
	OverallPrizes        *[]string `json:"overall_prizes"`
	GalleryOpen          *bool     `json:"gallery_open"`
	SubmissionsPublic    *bool     `json:"submissions_public"`
	// when judging closes.
	JudgingEndsAt *string `json:"judging_ends_at"`
}

func (h *HackHandler) patchEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req patchEventReq
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	updated, deadlineChanged, err := h.events.PatchEvent(r.Context(), a.event.ID, events.EventPatch(req))
	if err != nil {
		writeHackError(w, err)
		return
	}
	if deadlineChanged {
		h.links.pinDue(r.Context(), a.event.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, h.views.eventView(r.Context(), updated, a.member, true))
}
