package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/sites"
)

// filesRequest is the JSON deploy body: a map of relative path -> file contents.
// This is the LLM-friendly path — a web LLM can emit one copy-paste request with
// the file contents inline, no archiving step. For binary assets or large sites,
// use the archive upload (POST/PUT /v1/sites/{name}).
type filesRequest struct {
	Access      *string           `json:"access"`
	Viewers     []string          `json:"viewers"`
	Files       map[string]string `json:"files"`
	FilesBase64 map[string]string `json:"files_base64"`
	RemoveFiles []string          `json:"remove_files"`
}

// readJSONFiles decodes a {"files":{path:contents}} body and runs it through the
// SAME path/secret/size guards as archive extraction (sites.SanitizeFiles +
// ValidateExtensions). Returns the file map and a content digest for the version
// row. On any error it has already written the HTTP response.
func (h *SiteHandler) readJSONFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	raw, _, limit, override, err := h.decodeJSONFiles(w, r)
	if err != nil {
		return nil, "", err
	}
	return h.finishJSONFiles(w, raw, limit, override)
}

func (h *SiteHandler) decodeJSONFiles(w http.ResponseWriter, r *http.Request) (map[string][]byte, []string, int64, bool, error) {
	limit, override := h.siteLimitFor(r.Context(), h.database, auth.GetUser(r.Context()))
	// The files themselves are held to the limit once decoded.
	bodyLimit := sites.JSONBodyLimit(limit)
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout(bodyLimit)))
	defer rc.SetReadDeadline(time.Time{})
	var req filesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var requestErr *sites.Error
		if errors.As(err, &requestErr) {
			writeJSON(w, 400, errorResponse{Code: requestErr.Code, Error: requestErr.Message})
			return nil, nil, limit, override, err
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeTooLarge(w, limit, "")
			return nil, nil, limit, override, err
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return nil, nil, limit, override, err
	}
	if !h.readCreateAccess(w, r, req.Access, req.Viewers) {
		return nil, nil, limit, override, errors.New("invalid creation access")
	}
	keep := r.Method == http.MethodPut && r.URL.Query().Get("keep") == "1"
	if req.RemoveFiles != nil && !keep {
		err := errors.New("remove_files requires keep=1")
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return nil, nil, limit, override, err
	}
	if len(req.Files) == 0 && !keep {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "files object is required and must be non-empty"})
		return nil, nil, limit, override, errors.New("empty files")
	}

	raw := make(map[string][]byte, len(req.Files)+len(req.FilesBase64))
	for path, contents := range req.Files {
		raw[path] = []byte(contents)
	}
	for path, contents := range req.FilesBase64 {
		if _, exists := req.Files[path]; exists {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("path %q is present in both files and files_base64", path)})
			return nil, nil, limit, override, errors.New("duplicate text and base64 path")
		}
		decoded, err := base64.StdEncoding.DecodeString(contents)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("invalid base64 for %q", path)})
			return nil, nil, limit, override, err
		}
		raw[path] = decoded
	}

	if keep && len(raw) > 0 {
		clean, _, err := h.sitesSvc().PrepareFiles(sites.FilePreparationInput{Files: raw, Limit: limit, Override: override, Patch: true})
		if err != nil {
			h.writePreparationError(w, err, limit, false)
			return nil, nil, limit, override, err
		}
		raw = clean
	}
	return raw, req.RemoveFiles, limit, override, nil
}

func (h *SiteHandler) finishJSONFiles(w http.ResponseWriter, raw map[string][]byte, limit int64, override bool) (map[string][]byte, string, error) {
	files, digest, err := h.sitesSvc().PrepareFiles(sites.FilePreparationInput{Files: raw, Limit: limit, Override: override})
	if err != nil {
		h.writePreparationError(w, err, limit, false)
		return nil, "", err
	}
	return files, digest, nil
}

// createSiteFiles is the JSON create path. Mirrors createSite's pre-checks, then
// shares commitNewSite.
func (h *SiteHandler) createSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !writeCreatePreflight(w, h.sitesSvc().CreatePreflight(r.Context(), sites.PreflightInput{
		User: user, Name: siteName, CheckName: true, Publish: r.URL.Query().Get("publish"),
	})) {
		return
	}

	files, digest, err := h.readJSONFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitCreate(w, r, user, siteName, files, digest, false)
}

// updateSiteFiles is the JSON update path. Mirrors updateSite's pre-checks, then
// shares commitSiteUpdate.
func (h *SiteHandler) updateSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	preflight, err := h.sitesSvc().UpdatePreflight(r.Context(), sites.UpdatePreflightInput{
		User: user, Name: siteName, Publish: r.URL.Query().Get("publish"), Create: r.URL.Query().Get("create"),
	})
	if !writeCreatePreflight(w, err) {
		return
	}
	publish, create := preflight.Publish, preflight.Create

	raw, remove, limit, override, err := h.decodeJSONFiles(w, r)
	if err != nil {
		return
	}
	keep := r.URL.Query().Get("keep") == "1"
	if keep {
		h.respondSiteUpdate(w, r, user, siteName, sites.UpdateInput{User: user, Name: siteName, Files: raw, Author: versionAuthor(r), Publish: publish, CreateIfMissing: create, Keep: true, Remove: remove, LimitOverride: override, Limit: limit}, func(site siteResponse) { writeJSON(w, http.StatusOK, site) })
		return
	}
	files, digest, err := h.finishJSONFiles(w, raw, limit, override)
	if err != nil {
		return
	}
	if create {
		h.createOrUpdateSite(w, r, user, siteName, files, digest, publish)
	} else {
		h.commitSiteUpdate(w, r, user, siteName, files, digest, publish)
	}
}
