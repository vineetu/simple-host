package siteshttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
)

// filesRequest is the JSON deploy body (relative path -> contents): one inline request with
// no archiving step. Binary assets or large sites use the archive upload.
type filesRequest struct {
	Access      *string           `json:"access"`
	Viewers     []string          `json:"viewers"`
	Files       map[string]string `json:"files"`
	FilesBase64 map[string]string `json:"files_base64"`
	RemoveFiles []string          `json:"remove_files"`
}

// createSiteFiles is POST /v1/sites/{sitename}/files.
func (h *SiteHandler) createSiteFiles(w http.ResponseWriter, r *http.Request) {
	h.deployFiles(w, r, true)
}

// UpdateSiteFiles is PUT /v1/sites/{sitename}/files; with ?keep=1 the files
// patch the live ones and remove_files drops live files.
func (h *SiteHandler) UpdateSiteFiles(w http.ResponseWriter, r *http.Request) {
	h.deployFiles(w, r, false)
}

func (h *SiteHandler) deployFiles(w http.ResponseWriter, r *http.Request, isNew bool) {
	target, ok := deployTarget(w, r, isNew)
	if !ok {
		return
	}
	adm, err := h.SitesSvc().AdmitDeploy(r.Context(), target)
	if err != nil {
		writeCreatePreflight(w, err)
		return
	}
	req, ok := h.decodeJSONFiles(w, r, adm.Limit)
	if !ok {
		return
	}
	in := deployInput(r, target, adm)
	in.Files, in.Remove, in.Keep = req.files, req.remove, req.keep
	result, err := h.SitesSvc().Deploy(r.Context(), in)
	h.writeDeploy(w, r, in, result, err)
}

// jsonFiles is a decoded JSON deploy body: its files (text and base64
// together), the live files to remove and whether it patches (keep=1).
type jsonFiles struct {
	files  map[string][]byte
	remove []string
	keep   bool
}

// decodeJSONFiles reads the JSON body under the upload limit and checks its
// shape; the access it names is checked by readCreateAccess. On a refusal
// the answer is already written.
func (h *SiteHandler) decodeJSONFiles(w http.ResponseWriter, r *http.Request, limit int64) (jsonFiles, bool) {
	bodyLimit := sites.JSONBodyLimit(limit) // the files themselves are held to the limit once decoded
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(httpx.BodyReadTimeout(bodyLimit)))
	defer rc.SetReadDeadline(time.Time{})
	var req filesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var requestErr *sites.Error
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &requestErr):
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Code: requestErr.Code, Error: requestErr.Message})
		case errors.As(err, &maxBytesErr):
			writeTooLarge(w, limit, "")
		default:
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body"})
		}
		return jsonFiles{}, false
	}
	if !h.readCreateAccess(w, r, req.Access, req.Viewers) {
		return jsonFiles{}, false
	}
	out := jsonFiles{remove: req.RemoveFiles, keep: r.Method == http.MethodPut && r.URL.Query().Get("keep") == "1"}
	if req.RemoveFiles != nil && !out.keep {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "remove_files requires keep=1"})
		return jsonFiles{}, false
	}
	if len(req.Files) == 0 && !out.keep {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "files object is required and must be non-empty"})
		return jsonFiles{}, false
	}
	files, refusal := req.decodedFiles()
	if refusal != "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: refusal})
		return jsonFiles{}, false
	}
	out.files = files
	return out, true
}

// decodedFiles is the body's files, text and base64 together; a refusal
// says why they cannot be read.
func (req filesRequest) decodedFiles() (map[string][]byte, string) {
	files := make(map[string][]byte, len(req.Files)+len(req.FilesBase64))
	for path, contents := range req.Files {
		files[path] = []byte(contents)
	}
	for path, contents := range req.FilesBase64 {
		if _, exists := req.Files[path]; exists {
			return nil, fmt.Sprintf("path %q is present in both files and files_base64", path)
		}
		decoded, err := base64.StdEncoding.DecodeString(contents)
		if err != nil {
			return nil, fmt.Sprintf("invalid base64 for %q", path)
		}
		files[path] = decoded
	}
	return files, ""
}
