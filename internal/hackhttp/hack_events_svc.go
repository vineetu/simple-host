package hackhttp

import (
	"errors"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"net/http"
)

func (h *HackHandler) eventsSvc() *events.Service { return events.New(h.database) }

func writeEventsError(w http.ResponseWriter, err error) {
	var workflowErr *events.Error
	if errors.As(err, &workflowErr) {
		httpx.WriteError(w, workflowErr.Status, workflowErr.Code, workflowErr.Message)
	} else {
		httpx.WriteInternal(w)
	}
}
