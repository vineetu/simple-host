package hackhttp

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/hack/members"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"net/http"
)

func (h *HackHandler) membersSvc() *members.Service {
	return members.New(h.database, h.archiveEventTx, h.deleteEventTx, func(ctx context.Context, ev model.Event) {
		papermetrics.RecordHackStage(ctx, ev.ID, ev.Stage, ev.UpdatedAt)
		papermetrics.HackPhasesChanged(ctx)
		h.pinDue(ctx, ev.ID)
		// requestEventCert is a no-op for archived events, as in setStage.
	}, h.removeEventFiles, h.removeOrphanTeamSites)
}

func writeMemberError(w http.ResponseWriter, err error) {
	if e, ok := err.(*members.Error); ok {
		status := http.StatusConflict
		switch e.Code {
		case "too_many_questions", "too_many_tracks", "invalid_question_id", "invalid_question", "invalid_track_slug", "invalid_track", "invalid_track_name", "invalid_challenge", "invalid_track_prize", "invalid_decision", "invalid_answers", "invalid_answer", "answer_required", "coc_required", "invalid_display_name", "invalid_city", "invalid_country", "invalid_age_range", "invalid_gender":
			status = http.StatusBadRequest
		}

		if e.Code == "event_taken_down" {
			status = http.StatusForbidden
		}
		if e.Code == "event_not_found" {
			writeEventNotFound(w)
			return
		}
		if e.Code == "track_not_found" {
			status = http.StatusNotFound
		}
		httpx.WriteError(w, status, e.Code, e.Message)
		return
	}
	httpx.WriteInternal(w)
}
