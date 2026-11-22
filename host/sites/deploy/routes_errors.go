package deploy

import (
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

func (h routes) writePreparationError(w http.ResponseWriter, err error, limit int64, archive bool) {
	var e *UploadError
	if !errors.As(err, &e) {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if e.TooLarge {
		sitesroute.WriteTooLarge(w, limit, err.Error())
		return
	}
	code, message := e.Code, err.Error()
	if archive && code == "invalid_archive" {
		message = "invalid site archive"
		if h.Ed.Storage().StrictArchives {
			message += ": " + err.Error()
		}
	}
	if code == "digest_mismatch" || code == "invalid_files" {
		code = ""
	}
	httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: message, Code: code})
}
