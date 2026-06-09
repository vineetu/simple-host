package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/tarball"
)

const maxSiteArchiveSize = 100 << 20
const maxSiteStateSize = 1 << 20

type SiteHandler struct {
	database     *sql.DB
	disk         *storage.DiskStorage
	siteDomain   string
	deployScript string
}

type siteResponse struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	ActiveVersion  int       `json:"active_version"`
	SiteURL        string    `json:"site_url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	OwnerUsername  string    `json:"owner_username,omitempty"`
	Note           string    `json:"note,omitempty"`
}

type versionResponse struct {
	VersionNumber int       `json:"version_number"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	IsActive      bool      `json:"is_active"`
}

func NewSiteHandler(database *sql.DB, disk *storage.DiskStorage, siteDomain, deployScript string) *SiteHandler {
	return &SiteHandler{
		database:     database,
		disk:         disk,
		siteDomain:   siteDomain,
		deployScript: deployScript,
	}
}

func (h *SiteHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/sites/{sitename}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createSite))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.updateSite))))
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteSite))))
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSites))))
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setActiveVersion))))

	// State routes are deliberately NOT wrapped — they serve browser pages
	// that parse the JSON state object directly. Adding _notice would
	// corrupt that contract.
	mux.HandleFunc("GET /v1/sites/{sitename}/state", h.getSiteState)
	mux.HandleFunc("PUT /v1/sites/{sitename}/state", h.putSiteState)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/state", h.optionsSiteState)
}

// originHostForSite returns the expected hostname for state CORS, e.g.
// "mysite.ideaflow.page".
func (h *SiteHandler) originHostForSite(siteName string) string {
	return siteName + "." + h.siteDomain
}

// authorizeStateOrigin checks Origin/Referer and, on a match, sets the CORS
// headers that allow the calling site to read the response. Returns true if
// the request is allowed.
func (h *SiteHandler) authorizeStateOrigin(w http.ResponseWriter, r *http.Request, siteName string) bool {
	want := h.originHostForSite(siteName)

	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}

	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Host != want {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	return true
}

func (h *SiteHandler) optionsSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) getSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	state, err := db.GetSiteState(r.Context(), h.database, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

func (h *SiteHandler) putSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteStateSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
		return
	}

	state := json.RawMessage(body)
	if err := db.UpdateSiteState(r.Context(), h.database, siteName, state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

func (h *SiteHandler) createSite(w http.ResponseWriter, r *http.Request) {
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

	files, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	siteURL := fmt.Sprintf("https://%s.%s", siteName, h.siteDomain)
	site, err := db.CreateSite(r.Context(), tx, user.ID, siteName, siteURL)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "site already exists"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	const versionNumber = 1
	diskPath := fmt.Sprintf("%s/v%d/", siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.WriteFiles(r.Context(), siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.UpdateCurrent(siteName, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = versionNumber

	// Queue for cortex-share registration (processed by deploy-watcher)
	if h.deployScript != "" {
		queueFile := h.disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, siteName); err != nil {
			log.Printf("failed to queue deploy for %s: %v", siteName, err)
		}
	}

	writeJSON(w, http.StatusCreated, toSiteResponse(site, ""))
}

func (h *SiteHandler) updateSite(w http.ResponseWriter, r *http.Request) {
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

	files, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	maxVersion, err := db.GetMaxVersionNumber(r.Context(), tx, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versionNumber := maxVersion + 1
	diskPath := fmt.Sprintf("%s/v%d/", siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.WriteFiles(r.Context(), siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.UpdateCurrent(siteName, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = versionNumber
	writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
}

// listVersions returns the full version history of a site for the
// authenticated owner. Each row includes the version number, status,
// created_at, and a flag for which one is currently active.
func (h *SiteHandler) listVersions(w http.ResponseWriter, r *http.Request) {
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

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	out := make([]versionResponse, len(versions))
	for i, v := range versions {
		out[i] = versionResponse{
			VersionNumber: v.VersionNumber,
			Status:        v.Status,
			CreatedAt:     v.CreatedAt,
			IsActive:      v.VersionNumber == site.ActiveVersion,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// setActiveVersion points the site's `current` symlink at the requested
// version and updates sites.active_version. Used for rollbacks (target
// older) and roll-forwards (target newer). Idempotent if the target is
// already active.
func (h *SiteHandler) setActiveVersion(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		VersionNumber int `json:"version_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.VersionNumber < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "version_number must be >= 1"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if site.ActiveVersion == req.VersionNumber {
		writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var found bool
	for _, v := range versions {
		if v.VersionNumber == req.VersionNumber {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}

	if err := h.disk.UpdateCurrent(site.Name, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), h.database, site.ID, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = req.VersionNumber
	writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
}

func (h *SiteHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
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

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	if err := db.DeleteSite(r.Context(), tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.DeleteSite(site.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	var sites []db.Site
	var err error

	if user.IsAdmin {
		sites, err = db.ListAllSites(r.Context(), h.database)
	} else {
		sites, err = db.ListSitesByUser(r.Context(), h.database, user.ID)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	response := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		response = append(response, toSiteResponse(site, ""))
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *SiteHandler) readAndValidateFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, error) {
	body, err := readLimitedBody(w, r)
	if err != nil {
		return nil, err
	}

	filename := archiveFilename(siteName, body)
	files, err := tarball.Extract(bytes.NewReader(body), filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site archive"})
		return nil, err
	}

	if err := tarball.ValidateExtensions(files); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, err
	}

	return files, nil
}

func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteArchiveSize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return nil, err
		}

		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return nil, err
	}

	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request body is required"})
		return nil, errors.New("empty request body")
	}

	return body, nil
}

func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}

	return siteName + ".tar.gz"
}

func toSiteResponse(site db.Site, note string) siteResponse {
	return siteResponse{
		ID:            site.ID,
		UserID:        site.UserID,
		Name:          site.Name,
		ActiveVersion: site.ActiveVersion,
		SiteURL:       site.SiteURL,
		CreatedAt:     site.CreatedAt,
		UpdatedAt:     site.UpdatedAt,
		OwnerUsername: site.OwnerUsername,
		Note:          note,
	}
}

func appendToFile(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
