package siteshttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func init() {
	if v := os.Getenv("KEEP_VERSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			web.DefaultKeepVersions = n
		}
	}
}

// The active version is never a candidate, which matters after a rollback: an
// instance keeping one version whose active is v3 out of v10 must not delete
// v4 through v10, because active is below them. It keeps more than asked for
// until the next deploy, which then makes active the newest again and clears
// the backlog in one pass. Keeping too much is recoverable; deleting the tree
// someone is looking at is not.
func pruneThreshold(activeVersion, keep int) (keepFrom int, ok bool) {
	if keep <= 0 {
		return 0, false
	}
	keepFrom = activeVersion - keep + 1
	if keepFrom < 2 {
		return 0, false // nothing below v1 exists to remove
	}
	return keepFrom, true
}

// maxSiteKeepVersions bounds the per-site setting. It is a retention count,
// not a quota; the bound only keeps a typo from reading as "keep forever".
const maxSiteKeepVersions = 1000

// setKeepVersions (PUT /v1/sites/{sitename}/keep-versions) sets how many deploys of one site are kept and prunes
// at once. 0 returns the site to the instance setting; N >= 1 keeps the newest N plus the live one. Lowering it
// deletes older versions for good, which is why a deploy-only key cannot call it (auth/scope.go).
func (h *SiteHandler) setKeepVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if err := h.SitesSvc().CheckRetentionAllowed(r.Context(), user); err != nil {
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
	site, effective, removed, err := h.SitesSvc().SetKeepVersions(r.Context(), sites.KeepVersionsInput{UserID: user.ID, Name: siteName, Keep: keep})
	if err != nil {
		writeSitesError(w, err, siteName)
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

// versionAuthor reads ?author= on a deploy route; a team member's deploy that names none is credited to
// the member's handle so the team sees who published it.
func versionAuthor(r *http.Request) string {
	s := sites.VersionAuthor(r.URL.Query().Get("author"))
	if u := auth.GetUser(r.Context()); s == "" && u != nil && u.Member != nil {
		s = u.Member.PersonHandle
	}
	return s
}

func pendingParam(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("pending"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// discardVersion (DELETE /v1/sites/{sitename}/versions/{version}) discards a version stored with publish=false
// that nobody has made live: its files go and its row stays as a tombstone (status 'discarded') so the number is
// never reused. The live version and any that has been live are refused (409): those leave through retention.
// A deploy-only key or site editor cannot call it (auth/scope.go). The admin mirror runs the same handler.
func (h *SiteHandler) discardVersion(w http.ResponseWriter, r *http.Request) {
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
	site, err := h.SitesSvc().DiscardVersion(r.Context(), sites.DiscardInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expectedSiteID, CheckSiteID: checkSiteID, Version: n})
	if err != nil {
		writeSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":              site.Name,
		"discarded_version": n,
		"active_version":    site.ActiveVersion,
	})
}
