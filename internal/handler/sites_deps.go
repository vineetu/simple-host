package handler

import (
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/hack/teams"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/app/sites"
)

// signInMailer is the outbound capability consumed by visitor sign-in.
type signInMailer interface {
	SendSignInCode(string, string, string) error
}

// writePreparationError retains each upload route's error shape and precedence.
func (h *SiteHandler) writePreparationError(w http.ResponseWriter, err error, limit int64, archive, patch bool) {
	var e *sites.UploadError
	if !errors.As(err, &e) {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}
	if e.TooLarge {
		writeTooLarge(w, limit, err.Error())
		return
	}
	code, message := e.Code, err.Error()
	if archive && code == "invalid_archive" {
		message = "invalid site archive"
		if h.ed.Storage().StrictArchives {
			message += ": " + err.Error()
		}
	}
	if code == "digest_mismatch" || code == "invalid_files" || patch {
		code = ""
	}
	writeJSON(w, 400, errorResponse{Error: message, Code: code})
}

func (h *SiteHandler) hackSiteSvc() *teams.Service { return teams.New(h.database) }

func (h *SiteHandler) siteReporting() *operations.Reporting {
	return operations.NewReporting(h.database)
}
