package hackhttp

import (
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"net/http"
)

func writeContentError(w http.ResponseWriter, err error) {
	var e *events.Error
	if !errors.As(err, &e) {
		httpx.WriteInternal(w)
		return
	}
	status := http.StatusConflict
	if e.Code == "announcement_not_found" {
		status = http.StatusNotFound
	}
	httpx.WriteError(w, status, e.Code, e.Message)
}
