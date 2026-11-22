package versions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

// routes are the versions handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the version routes: the list, a version's files, Make
// active, discarding a pending version, owner preview links and keep-versions,
// and their admin mirrors by site id (run as the owner). siteOpLimiter is the
// limiter shared with the other site operations.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	v := routes{h}
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(v.listVersions))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files", noticeMiddleware(authMiddleware(http.HandlerFunc(v.listVersionFiles))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files/{path...}", noticeMiddleware(authMiddleware(http.HandlerFunc(v.getVersionFile))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(v.setActiveVersion))))
	// Not in auth/scope.go deployRoutes or editorRoutes on purpose: a
	// deploy-only key or a site editor cannot remove a version.
	mux.Handle("DELETE /v1/sites/{sitename}/versions/{version}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(v.discardVersion)))))
	mux.Handle("POST /v1/sites/{sitename}/versions/{version}/preview-link", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(v.createPreviewLink)))))
	// 0 = the instance's KEEP_VERSIONS; setting it prunes at once.
	mux.Handle("PUT /v1/sites/{sitename}/keep-versions", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(v.setKeepVersions)))))
	mux.Handle("GET /v1/admin/sites/{id}/versions", authMiddleware(h.AsSiteOwner(v.listVersions)))
	mux.Handle("PUT /v1/admin/sites/{id}/active-version", authMiddleware(h.AsSiteOwner(v.setActiveVersion)))
	// An admin sees a version of a site behind a passcode through a preview
	// link without knowing the passcode; logged like every admin call.
	mux.Handle("POST /v1/admin/sites/{id}/versions/{version}/preview-link", authMiddleware(h.AsSiteOwner(v.createPreviewLink)))
	mux.Handle("DELETE /v1/admin/sites/{id}/versions/{version}", authMiddleware(h.AsSiteOwner(v.discardVersion)))
}

func (h routes) listVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	expectedID, checkID := r.Context().Value(web.AdminSiteKey{}).(string)
	// ?pending=1: only versions stored with publish=false that nobody has
	// made live yet, the ones discardVersion can remove.
	versions, err := h.svc().ListVersions(r.Context(), VersionListInput{UserID: user.ID, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Pending: sitesroute.PendingParam(r)})
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}
	out := make([]sitesroute.VersionResponse, 0, len(versions))
	for _, v := range versions {
		out = append(out, sitesroute.VersionResponse{VersionNumber: v.VersionNumber, Status: v.Status, Author: v.Author, CreatedAt: v.CreatedAt, IsActive: v.Active})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setActiveVersion points `current` at the requested version and updates sites.active_version (rollback or
// roll-forward). Idempotent if the target is already active.
func (h routes) setActiveVersion(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	var req struct {
		VersionNumber int `json:"version_number"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if req.VersionNumber < 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "version_number must be >= 1"})
		return
	}

	expectedID, checkID := r.Context().Value(web.AdminSiteKey{}).(string)
	result, err := h.svc().Rollback(r.Context(), RollbackInput{User: user, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Version: req.VersionNumber, IfMatch: r.Header.Get("If-Match")})
	if err != nil {
		var gateErr *sites.GateError
		if errors.As(err, &gateErr) {
			sitesroute.WriteRollbackGateError(w, gateErr)
		} else {
			sitesroute.WriteSitesError(w, err, siteName)
		}
		return
	}
	if result.Moved {
		h.EmitDeployed(result.Site.ID, result.Site.ActiveVersion, result.PreviousVersion, "active_version")
	}
	httpx.WriteJSON(w, http.StatusOK, h.SiteResponseFor(result.Site, result.FileBytes))
}

// createPreviewLink is POST /v1/sites/{sitename}/versions/{version}/preview-link: an hour-long owner preview address.
func (h routes) createPreviewLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	expectedSiteID, checkSiteID := r.Context().Value(web.AdminSiteKey{}).(string)
	site, err := h.svc().KeptVersion(r.Context(), user.ID, name, expectedSiteID, checkSiteID, n)
	if err != nil {
		sitesroute.WriteSitesError(w, err, name)
		return
	}
	link, expires, ok := h.PreviewLink(site, n)
	if !ok {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: h.NoPreviewWhy(), Code: "preview_unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":       site.Name,
		"version":    n,
		"live":       n == site.ActiveVersion,
		"url":        link,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"expires_in": int(h.Limits.PreviewLinkTTL.Seconds()),
	})
}

func init() {
	if v := os.Getenv("KEEP_VERSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			web.DefaultKeepVersions = n
		}
	}
}

// maxSiteKeepVersions bounds the per-site setting. It is a retention count,
// not a quota; the bound only keeps a typo from reading as "keep forever".
const maxSiteKeepVersions = 1000

// setKeepVersions (PUT /v1/sites/{sitename}/keep-versions) sets how many deploys of one site are kept and prunes
// at once. 0 returns the site to the instance setting; N >= 1 keeps the newest N plus the live one. Lowering it
// deletes older versions for good, which is why a deploy-only key cannot call it (auth/scope.go).
func (h routes) setKeepVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if err := h.svc().CheckRetentionAllowed(r.Context(), user); err != nil {
		siteErr := err.(*sites.Error)
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	var req struct {
		KeepVersions *int `json:"keep_versions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.KeepVersions == nil ||
		*req.KeepVersions < 0 || *req.KeepVersions > maxSiteKeepVersions {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{
			Error: fmt.Sprintf(`body must be {"keep_versions": N}: N from 1 to %d keeps the newest N versions plus the live one; 0 uses this server's setting`, maxSiteKeepVersions),
			Code:  "invalid_request",
		})
		return
	}
	keep := *req.KeepVersions
	site, effective, removed, err := h.svc().SetKeepVersions(r.Context(), KeepVersionsInput{UserID: user.ID, Name: siteName, Keep: keep})
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":                    site.Name,
		"keep_versions":           keep,
		"effective_keep_versions": effective,
		"active_version":          site.ActiveVersion,
		"removed_versions":        removed,
	})
}

// discardVersion (DELETE /v1/sites/{sitename}/versions/{version}) discards a version stored with publish=false
// that nobody has made live: its files go and its row stays as a tombstone (status 'discarded') so the number is
// never reused. The live version and any that has been live are refused (409): those leave through retention.
// A deploy-only key or site editor cannot call it (auth/scope.go). The admin mirror runs the same handler.
func (h routes) discardVersion(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found", Code: "version_not_found"})
		return
	}
	expectedSiteID, checkSiteID := r.Context().Value(web.AdminSiteKey{}).(string)
	site, err := h.svc().DiscardVersion(r.Context(), DiscardInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expectedSiteID, CheckSiteID: checkSiteID, Version: n})
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":              site.Name,
		"discarded_version": n,
		"active_version":    site.ActiveVersion,
	})
}

var errInvalidVersionPath = sites.ErrInvalidFilePath

type versionTextsResponse struct {
	VersionNumber int        `json:"version_number"`
	IsActive      bool       `json:"is_active"`
	CreatedAt     time.Time  `json:"created_at"`
	Files         []FileText `json:"files"`
}

type versionFilesResponse struct {
	VersionNumber int                      `json:"version_number"`
	IsActive      bool                     `json:"is_active"`
	CreatedAt     time.Time                `json:"created_at"`
	Files         []sitesroute.VersionFile `json:"files"`
}

func (h routes) listVersionFiles(w http.ResponseWriter, r *http.Request) {
	h.readVersionFiles(w, r, true)
}

func (h routes) getVersionFile(w http.ResponseWriter, r *http.Request) {
	h.readVersionFiles(w, r, false)
}

func (h routes) readVersionFiles(w http.ResponseWriter, r *http.Request, listing bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	// {version} "live" (listing only) is the site's live version.
	live := listing && r.PathValue("version") == "live"
	n, err := strconv.Atoi(r.PathValue("version"))
	if !live && (err != nil || n < 1) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid version"})
		return
	}
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sitesroute.WriteSiteNotFound(w, siteName)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	if live {
		if n = site.ActiveVersion; n < 1 {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "the site has no live version", Code: "no_live_version"})
			return
		}
	}
	stored, err := h.SitesSvc().ListVersionsBySite(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	serveVersionFiles(w, r, h.Disk, sites.FileVersion{UserID: user.ID, SiteName: siteName, Version: n}, stored, n, site.ActiveVersion, listing)
}

// serveVersionFiles checks retained metadata as well as disk: a pruned version
// must not be readable even if its directory has not yet been removed.
func serveVersionFiles(w http.ResponseWriter, r *http.Request, files sites.SiteFiles, ref sites.FileVersion, stored []model.Version, n, active int, listing bool) {
	var version *model.Version
	for i := range stored {
		if stored[i].VersionNumber == n {
			version = &stored[i]
			break
		}
	}
	if version == nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	_, err := files.StatVersion(r.Context(), ref)
	if os.IsNotExist(err) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	if listing && r.URL.Query().Has("read") {
		// ?read=<path> (repeated, up to sites.MaxReadPaths): those files' text in
		// place of the listing.
		paths := r.URL.Query()["read"]
		if len(paths) > sites.MaxReadPaths {
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "read takes at most " + strconv.Itoa(sites.MaxReadPaths) + " paths", Code: "invalid_request"})
			return
		}
		texts, err := ReadVersionTexts(r.Context(), files, ref, paths)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		// File contents, like the raw file route: never stored by a cache.
		w.Header().Set("Cache-Control", "private, no-store")
		httpx.WriteJSON(w, http.StatusOK, versionTextsResponse{n, n == active, version.CreatedAt, texts})
		return
	}
	if listing {
		entries, err := sitesroute.WalkVersionFiles(r.Context(), files, ref)
		if err != nil {
			writeVersionFileError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, versionFilesResponse{n, n == active, version.CreatedAt, entries})
		return
	}
	if !sites.ValidVersionFilePath(r.PathValue("path")) {
		writeVersionFileError(w, errInvalidVersionPath)
		return
	}
	file, info, err := files.OpenFile(r.Context(), ref, r.PathValue("path"))
	if err != nil {
		writeVersionFileError(w, err)
		return
	}
	defer file.Close()
	if info.IsDir() {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "file not found"})
		return
	}
	w.Header().Set("Content-Type", VersionFileType(r.PathValue("path")))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("serve version file %s: %v", r.PathValue("path"), err)
	}
}

func writeVersionFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidVersionPath):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid path"})
	case os.IsNotExist(err), errors.Is(err, syscall.ENOTDIR):
		// ENOTDIR: a path component is a regular file (files/index.html/child).
		// To the caller that is simply a file that does not exist.
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "file not found"})
	default:
		httpx.WriteInternal(w)
	}
}

// svc is the versions workflow on this request's site service.
func (h routes) svc() Service { return Service{Service: h.SitesSvc()} }
