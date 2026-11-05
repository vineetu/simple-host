package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// Reminder offset settings (0003-hack-notices-judges-messages.sql). One list
// per event drives both audiences: entry reminders to participants and
// judging reminders to judges.

func (h *HackHandler) registerReminders(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/reminders", wrap(h.getReminderSettings))
	mux.Handle("PUT /v1/hack/events/{slug}/reminders", wrap(h.putReminderSettings))
}

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
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reminderSettingsJSON(a.event))
}

func (h *HackHandler) putReminderSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	// OffsetsMinutes is a pointer so a missing field (nil) can be told apart
	// from an explicit [] (valid: turns reminders off for both audiences).
	// Without this, {} decodes OffsetsMinutes to the same nil-turned-empty
	// slice as [] and silently turns reminders off.
	var req struct {
		OffsetsMinutes *[]int `json:"offsets_minutes"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.OffsetsMinutes == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_offsets", "offsets_minutes is required ([] turns reminders off)")
		return
	}
	updated, err := h.eventsSvc().SetReminderOffsets(r.Context(), a.event.ID, a.user.ID, *req.OffsetsMinutes)
	if err != nil {
		writeEventsError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reminderSettingsJSON(updated))
}
