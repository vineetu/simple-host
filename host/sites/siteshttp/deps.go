package siteshttp

import (
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

// writePreparationError retains each upload route's error shape and precedence.
func (h *SiteHandler) writePreparationError(w http.ResponseWriter, err error, limit int64, archive bool) {
	var e *sites.UploadError
	if !errors.As(err, &e) {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if e.TooLarge {
		writeTooLarge(w, limit, err.Error())
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

func (h *SiteHandler) siteReporting() *operations.Reporting {
	return operations.NewReporting(h.Database)
}
