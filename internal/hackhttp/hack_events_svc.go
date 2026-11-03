package hackhttp

import (
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
)

func (h *HackHandler) eventsSvc() *events.Service { return events.New(h.database) }

func writeEventsError(w http.ResponseWriter, err error) {
	var workflowErr *events.Error
	if !errors.As(err, &workflowErr) {
		httpx.WriteInternal(w)
		return
	}
	status := http.StatusConflict
	switch workflowErr.Code {
	case "event_not_found", "invalid_code":
		status = http.StatusNotFound
	case "event_taken_down":
		status = http.StatusForbidden
	case "too_many_events_today":
		status = http.StatusTooManyRequests
	case "event_closed", "too_many_active_events", "name_taken", "archive_needs_participants", "delete_only_empty", "submissions_closed_stage", "judging_closed", "joining_closed", "already_member", "primary_organiser":
	default:
		status = http.StatusBadRequest
	}
	httpx.WriteError(w, status, workflowErr.Code, workflowErr.Message)
}
