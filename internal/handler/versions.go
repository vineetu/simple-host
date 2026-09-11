package handler

import (
	"context"
	"log"
	"os"
	"strconv"

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

// pruneVersions drops deploy history beyond the retention setting. Called after
// the deploy has committed and been promoted, never before: this is cleanup, and
// it must not be able to fail a deploy that has already succeeded.
func (h *SiteHandler) pruneVersions(ctx context.Context, siteID, userID, siteName string, activeVersion int) {
	keepFrom, ok := pruneThreshold(activeVersion, keepVersions)
	if !ok {
		return
	}
	removed, err := db.PruneVersions(ctx, h.database, siteID, keepFrom, activeVersion)
	if err != nil {
		log.Printf("prune versions for %s: %v", siteName, err)
		return
	}
	for _, versionNum := range removed {
		if err := h.disk.DeleteVersion(userID, siteName, versionNum); err != nil {
			// The row is already gone, so nothing can serve this version and
			// nothing will try. It is wasted space until someone notices.
			log.Printf("prune versions for %s: v%d row removed but files remain: %v", siteName, versionNum, err)
		}
	}
}
