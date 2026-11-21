package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Reminder offset settings. One list per event drives both audiences: entry reminders to participants and judging reminders to judges.

func reminderSettingsJSON(ev model.Event) map[string]any {
	offsets := ev.ReminderOffsets
	if offsets == nil {
		offsets = []int64{}
	}
	return map[string]any{
		"offsets_minutes": offsets,
		"default_minutes": events.DefaultReminderOffsets,
		"min_minutes":     events.ReminderOffsetMin,
		"max_minutes":     events.ReminderOffsetMax,
		"max_count":       events.ReminderOffsetCount,
		"judging_ends_at": rfc3339UTC(ev.JudgingEndsAt),
	}
}

func (h *HackHandler) getReminderSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reminderSettingsJSON(a.event))
}

func (h *HackHandler) putReminderSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	// OffsetsMinutes is a pointer so a missing field (nil) can be told apart from
	// an explicit [] (valid: turns reminders off for both audiences).
	var req struct {
		OffsetsMinutes *[]int `json:"offsets_minutes"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if req.OffsetsMinutes == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_offsets", "offsets_minutes is required ([] turns reminders off)")
		return
	}
	updated, err := h.events.SetReminderOffsets(r.Context(), a.event.ID, a.user.ID, *req.OffsetsMinutes)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reminderSettingsJSON(updated))
}
