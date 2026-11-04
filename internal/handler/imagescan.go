package handler

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
	"log"
	"net/http"
	"regexp"
	"strconv"
)

type ImageScan = operations.ImageScan
type ImageScanConfig = operations.ImageScanConfig

// SetImageScan connects the worker to the site side (site locks, addresses,
// take-down markers).
func (h *SiteHandler) SetImageScan(s *ImageScan) {
	h.imageScan = s
	if s != nil {
		s.SetSiteHooks(operations.ImageScanSiteHooks{Lock: h.lockSite, URL: h.siteURLFor})
	}
}

// imageScanOn is read by the privacy page: the image-matching line is served
// only while the feature is on (SetImageScanOn, from main).
var imageScanOn bool

// SetImageScanOn records whether image matching is on for the served pages.
// The privacy page is assembled once and cached (chromePageRaw strips the
// line there), so a change drops its cached copies.
func SetImageScanOn(on bool) {
	imageScanOn = on
	chromeCache.Range(func(k, _ any) bool {
		if k.(chromeCacheKey).name == "privacy.html" {
			chromeCache.Delete(k)
		}
		return true
	})
}

var imageScanPrivacyItem = regexp.MustCompile(`(?s)[ \t]*<li data-sh="image-scan">.*?</li>[ \t]*\r?\n?`)

// stripImageScanItem removes the privacy page's image-matching line while
// the feature is off. Called once per cached page (chromePageRaw).
func stripImageScanItem(page []byte) []byte {
	if imageScanOn {
		return page
	}
	return imageScanPrivacyItem.ReplaceAll(page, nil)
}

// siteWriterID is the account of the person behind a deploy. On Simple Hack
// the site belongs to the event's holding account, so a team key names its
// member and an organiser's publish names the organiser.
func siteWriterID(u *model.User) string {
	switch {
	case u.Team != nil && u.Team.MemberID != "":
		return u.Team.MemberID
	case u.EventWebsite != nil && u.EventWebsite.OrganiserID != "":
		return u.EventWebsite.OrganiserID
	}
	return u.ID
}

// storageUploader is who wrote a files-storage object, as a report names
// them: "owner" for the site's owner, "user:<id>" for another account
// writing with owner rights (a Simple Hack team member or organiser), else
// the visitor's id ("" when the visitor was not signed in).
func storageUploader(c storageCall) string {
	if c.owner {
		if c.uploaderID != "" && c.uploaderID != c.ownerID {
			return "user:" + c.uploaderID
		}
		return "owner"
	}
	return c.stampID()
}

// adminImageScan is GET /v1/admin/image-scan: the feature's state, the
// queue, every match whose report is still open, and the newest matches. Matches are listed whether or not the
// feature is on: a record outlives a config change.
func (h *SiteHandler) adminImageScan(w http.ResponseWriter, r *http.Request) {
	out, err := h.reporting.ImageScanSummary(r.Context(), h.imageScan)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, 200, out)
}

// adminImageScanReport is POST /v1/admin/image-scan/{id}/report.
func (h *SiteHandler) adminImageScanReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
		return
	}
	if h.imageScan == nil {
		if err := h.reporting.ImageMatchExists(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "image matching is off; set PHOTODNA_API_KEY, IMAGE_SCAN_REPORT_ORG, IMAGE_SCAN_REPORT_NAME, and IMAGE_SCAN_REPORT_EMAIL to file reports from here", Code: "report_unavailable"})
		return
	}
	rid, at, err := h.imageScan.FileReport(r.Context(), id)
	var saveErr *operations.ReportSaveError
	switch {
	case errors.As(err, &saveErr):
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error(), Code: "report_failed"})
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
	case errors.Is(err, operations.ErrReportAlreadyFiled):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this match was already reported as " + rid, Code: "already_reported"})
	case errors.Is(err, operations.ErrReportUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error(), Code: "report_unavailable"})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "the report could not be filed: " + err.Error(), Code: "report_failed"})
	default:
		admin := auth.GetUser(r.Context())
		log.Printf("admin_image_scan_report match=%d report_id=%s by=%s", id, rid, admin.ID)
		writeJSON(w, http.StatusOK, map[string]any{"report_id": rid, "reported_at": at})
	}
}
