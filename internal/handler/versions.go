package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
)

// keepVersions is the instance retention (0 keeps all). Hosted Simple Host
// sets 4; self-hosters retain the existing unset default and Simple Hack sets 2.
// Account and site defaults are resolved in accountRetention.
var keepVersions = 0

func init() {
	if v := os.Getenv("KEEP_VERSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			keepVersions = n
		}
	}
}

// KeepVersions reports the retention setting: how many deploys of a site are
// kept, or 0 for all of them.
func KeepVersions() int { return keepVersions }

// versionCopiesPerSite is what one site costs on disk, as a multiple of its
// size: every retained version, plus the live `current` tree, which is a full
// copy rather than a link. Capacity planning needs this number and getting it
// wrong is what made the first version of the sizing arithmetic wrong by an
// order of magnitude.
func versionCopiesPerSite() int {
	if keepVersions <= 0 {
		// Unbounded history. Assume a working ten for planning purposes —
		// there is no true answer, and pretending there is one would be worse.
		return 11
	}
	return keepVersions + 1
}

// pruneThreshold returns the lowest version number to keep, and whether there
// is anything to remove at all.
//
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

// effectiveKeepVersions is the retention that applies to one site: its own
// setting (sites.keep_versions) when it has one, else the instance's
// KEEP_VERSIONS. 0 means keep every version.
func effectiveKeepVersions(siteKeep int) int {
	return sites.EffectiveKeepVersions(siteKeep, keepVersions)
}

// pruneVersions drops deploy history beyond the site's retention (its own
// keep_versions, else the instance setting). Called after the deploy has
// committed and been promoted, never before: this is cleanup, and it must not
// be able to fail a deploy that has already succeeded. It is also what
// PUT /v1/sites/{sitename}/keep-versions runs to apply a new setting at once.
// Rows go first, then the folders: once the row is gone nothing can serve
// or roll back to that version. It returns the version numbers removed.
func (h *SiteHandler) pruneVersions(ctx context.Context, siteID, userID, siteName string, activeVersion, siteKeep int) []int {
	return h.sitesSvc().PruneVersions(ctx, siteID, userID, siteName, activeVersion, siteKeep)
}

// setKeepVersions sets how many deploys of one site are kept and applies it at
// once, through the same prune a deploy runs.
//
// PUT /v1/sites/{sitename}/keep-versions  {"keep_versions": N}
//
// 0 returns the site to the instance setting (KEEP_VERSIONS); N >= 1 keeps the
// newest N plus always the live one. Lowering it deletes older versions for
// good, which is why a deploy-only key cannot call it (auth/scope.go).
func (h *SiteHandler) setKeepVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if !h.limits.CanSetKeepVersions(accountHandles(r.Context(), h.database, user)...) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: fmt.Sprintf("Simple Host keeps your %d latest versions", keepVersions), Code: "keep_versions_fixed"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	var req struct {
		KeepVersions *int `json:"keep_versions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.KeepVersions == nil ||
		*req.KeepVersions < 0 || *req.KeepVersions > maxSiteKeepVersions {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: fmt.Sprintf(`body must be {"keep_versions": N}: N from 1 to %d keeps the newest N versions plus the live one; 0 uses this server's setting`, maxSiteKeepVersions),
			Code:  "invalid_request",
		})
		return
	}
	keep := *req.KeepVersions
	site, effective, removed, err := h.sitesSvc().SetKeepVersions(r.Context(), user.ID, siteName, keep)
	if err != nil {
		writeSitesError(w, err, siteName)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":                    site.Name,
		"keep_versions":           keep,
		"effective_keep_versions": effective,
		"active_version":          site.ActiveVersion,
		"removed_versions":        removed,
	})
}

// maxVersionAuthorRunes bounds ?author= on a deploy: a name or a short note,
// not a changelog.
const maxVersionAuthorRunes = 80

// versionAuthor reads ?author= on a deploy route: who made the version, kept
// on the row and shown in the version list. Spaces around it and control
// characters are dropped and it is cut to maxVersionAuthorRunes whole runes;
// "" when the deploy did not say.
func versionAuthor(r *http.Request) string {
	s := strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, r.URL.Query().Get("author"))
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxVersionAuthorRunes {
		runes := []rune(s)
		s = strings.TrimSpace(string(runes[:maxVersionAuthorRunes]))
	}
	return s
}

// pendingParam reads ?pending= on the version list: 1, true or yes narrows it
// to versions stored with publish=false that nobody has made live.
func pendingParam(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("pending"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// discardVersion discards one version a deploy stored with publish=false and
// nobody has made live since: its files go, and its row stays as a tombstone
// (status 'discarded') so the number is never handed to a later deploy.
//
// DELETE /v1/sites/{sitename}/versions/{version}
//
// The live version and any version that has been live are refused (409):
// those leave through retention (keep_versions), never by hand. A deploy-only
// or team key cannot call this (auth/scope.go lists neither route). The admin
// mirror DELETE /v1/admin/sites/{id}/versions/{version} runs the same handler.
func (h *SiteHandler) discardVersion(w http.ResponseWriter, r *http.Request) {
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
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found", Code: "version_not_found"})
		return
	}
	expectedSiteID, checkSiteID := r.Context().Value(adminSiteKey{}).(string)
	site, err := h.sitesSvc().DiscardVersion(r.Context(), user.ID, siteName, expectedSiteID, checkSiteID, n)
	if err != nil {
		writeSitesError(w, err, siteName)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site":              site.Name,
		"discarded_version": n,
		"active_version":    site.ActiveVersion,
	})
}
