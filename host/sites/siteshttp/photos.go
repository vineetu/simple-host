package siteshttp

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Process-wide: token links reach this same handler. Never queue body buffers.
var PhotoSaves atomic.Int64

func AcquirePhotoSave() bool {
	if PhotoSaves.Add(1) > int64(photoLimit("PHOTO_MAX_CONCURRENT", 3)) {
		PhotoSaves.Add(-1)
		return false
	}
	return true
}

func photoLimit(key string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

type photoResult struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type photoBatchResult struct {
	Site       string        `json:"site"`
	Version    int           `json:"version"`
	Photos     []photoResult `json:"photos"`
	Reconciled bool          `json:"reconciled,omitempty"`
}

func (h *SiteHandler) AddPhotos(w http.ResponseWriter, r *http.Request) {
	// Dashboard authentication is an explicit X-API-Key, never a visitor cookie.
	// Reuse the account sign-in origin policy for this apex browser action.
	if base := web.PublicBaseOrigin(h.publicBaseURL); base != nil && !web.AccountAuthAllowed(r, base) {
		web.WriteOriginRefused(w)
		return
	}
	if !AcquirePhotoSave() {
		w.Header().Set("Retry-After", "2")
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "Busy, try again in a moment"})
		return
	}
	defer PhotoSaves.Add(-1)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(time.Duration(photoLimit("PHOTO_BODY_TIMEOUT_SECONDS", 60)) * time.Second))
	defer rc.SetReadDeadline(time.Time{})
	user := auth.GetUser(r.Context())
	name := r.PathValue("sitename")
	if _, err := h.SitesSvc().PhotoTarget(r.Context(), user.ID, name); err != nil {
		writePhotoTargetError(w, r, name, err)
		return
	}

	bodyCap := sites.PhotoBodyLimit()
	if r.ContentLength > bodyCap {
		httpx.WriteJSON(w, 413, httpx.ErrorResponse{Error: "The photo upload is too large."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyCap)
	reader, err := r.MultipartReader()
	if err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "Choose photos to upload."})
		return
	}
	uploads, err := sites.ReadPhotoUploads(reader)
	if err != nil {
		status := http.StatusBadRequest
		if e, ok := err.(*sites.PhotoUploadError); ok && e.TooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	input := sites.UpdateInput{User: user, Name: name, Files: map[string][]byte{}, Photos: sites.UploadedPhotos(uploads), Reconcile: r.Header.Get("X-Photo-Reconcile") == "1", Publish: true}
	input.ExpectedID, input.CheckID = r.Context().Value(web.AdminSiteKey{}).(string)
	result, err := h.SitesSvc().Update(r.Context(), input)
	if err != nil {
		input.Limit = result.Limit
		h.writePhotoSaveError(w, r, input, err)
		return
	}
	if result.Published {
		h.noteDeployed(r.Context(), user, result)
	}
	response := h.siteResponseFromState(result.Site, result.ResponseState.(siteResponseState))
	results := make([]photoResult, 0, len(uploads))
	for i, p := range uploads {
		path := result.PhotoPaths[i]
		results = append(results, photoResult{Path: path, URL: strings.TrimRight(response.SiteURL, "/") + "/" + path, Bytes: len(p.Data), Width: p.Width, Height: p.Height})
	}
	httpx.WriteJSON(w, 200, photoBatchResult{name, result.Site.ActiveVersion, results, result.Reconciled})
}

func writePhotoTargetError(w http.ResponseWriter, r *http.Request, name string, err error) {
	if e, ok := err.(*sites.Error); ok && e.Code == "invalid_name" {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: e.Error()})
	} else if errors.Is(err, sql.ErrNoRows) {
		writeDeploySiteNotFound(w, r, name)
	} else {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "Could not read this site."})
	}
}

func (h *SiteHandler) writePhotoSaveError(w http.ResponseWriter, r *http.Request, input sites.UpdateInput, err error) {
	var siteErr *sites.Error
	if !errors.As(err, &siteErr) {
		h.writeSiteUpdateError(w, r, input.Name, input, err)
		return
	}
	switch siteErr.Code {
	case "photo_save_uncertain":
		httpx.WriteJSON(w, 409, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
	case "photo_conflict":
		httpx.WriteJSON(w, 409, httpx.ErrorResponse{Error: siteErr.Message})
	case "photo_read_failed":
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "Could not read this site."})
	case "photo_allocation_cancelled":
	default:
		h.writeSiteUpdateError(w, r, input.Name, input, err)
	}
}
