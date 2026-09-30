package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// keepVersions is how many deploys of a site are retained. Zero means keep
// every one, which is what simple-host.app has always done and what a general
// instance with disk to spare should keep doing.
//
// An event box is the opposite case. Nothing pruned versions, so a site on disk
// cost its size multiplied by its whole deploy history — and an agent redeploys
// a site a dozen times in an afternoon. On a 25 GB machine that history, not the
// sites, is what fills the disk. deploy/install/install.sh sets this to 1.
//
// The trade is rollback: at 1 there is no earlier version to go back to. For a
// hackathon that is the right trade, because the participant's agent still has
// the files and redeploying is one sentence.
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
	if siteKeep > 0 {
		return siteKeep
	}
	return keepVersions
}

// pruneVersions drops deploy history beyond the site's retention (its own
// keep_versions, else the instance setting). Called after the deploy has
// committed and been promoted, never before: this is cleanup, and it must not
// be able to fail a deploy that has already succeeded. It is also what
// PUT /v1/sites/{sitename}/keep-versions runs to apply a new setting at once.
// Rows go first, then the folders: once the row is gone nothing can serve
// or roll back to that version. It returns the version numbers removed.
func (h *SiteHandler) pruneVersions(ctx context.Context, siteID, userID, siteName string, activeVersion, siteKeep int) []int {
	keepFrom, ok := pruneThreshold(activeVersion, effectiveKeepVersions(siteKeep))
	if !ok {
		return nil
	}
	removed, err := db.PruneVersions(ctx, h.database, siteID, keepFrom, activeVersion)
	if err != nil {
		log.Printf("prune versions for %s: %v", siteName, err)
		return nil
	}
	for _, versionNum := range removed {
		if err := h.disk.DeleteVersion(userID, siteName, versionNum); err != nil {
			// The row is already gone, so nothing can serve this version and
			// nothing will try. It is wasted space until someone notices.
			log.Printf("prune versions for %s: v%d row removed but files remain: %v", siteName, versionNum, err)
		}
	}
	return removed
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
	// Under the same per-site lock as deploy, rollback, rename and delete, and
	// with the site read only once it is held: a rollback copying an older
	// version into place, or a delete and re-create of the name, must not have
	// that version's folder removed underneath it.
	unlock := h.lockSite(user.ID, siteName)
	defer unlock()
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	keep := *req.KeepVersions
	if err := db.SetSiteKeepVersions(r.Context(), h.database, site.ID, keep); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	removed := h.pruneVersions(r.Context(), site.ID, site.UserID, site.Name, site.ActiveVersion, keep)
	if removed == nil {
		removed = []int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":                    site.Name,
		"keep_versions":           keep,
		"effective_keep_versions": effectiveKeepVersions(keep),
		"active_version":          site.ActiveVersion,
		"removed_versions":        removed,
	})
}
