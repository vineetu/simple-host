package handler

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Small helpers both the Hack event API (hack.go and friends, which move
// to internal/hackhttp in the next slice) and this file's staying
// SiteHandler-side Hack code (hack_sites.go, hack_mode.go,
// hack_administration_sites.go, hack_paper_metrics.go) need. They are
// duplicated rather than shared across that future package boundary —
// hackhttp must never import handler (3.2-common.md) — rather than
// rewritten everywhere right now.
//
// Transitional (cleanup 3.2): proposed home internal/httpx (writeJSON and
// errorResponse, which these build on, are already moving there per slice
// 1a) or internal/rules/hackevents for the pure ones. Fold these in next
// slice instead of duplicating further.

// hackSitesEventNotFound is hack.go's writeEventNotFound, kept here for
// hack_sites.go's team-site routes so they do not need hack.go's
// writeHackErr once that file moves. Same status, code and message.
func hackSitesEventNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "event not found", Code: "event_not_found"})
}

// formatRFC3339 is hack.go's rfc3339Time, kept here for cohorts.go's signup
// export, which is not Hack-specific. Same formatting (UTC, RFC 3339).
func formatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// hackSitesGallerySlug is hack_wire.go's hackGallerySlug: one label,
// lowercase letters, digits and hyphens, at most 30 characters, not
// starting or ending with a hyphen.
var hackSitesGallerySlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func hackSitesGallerySlug(s string) bool {
	return len(s) <= 30 && hackSitesGallerySlugRe.MatchString(s)
}

// hackSitesScreenshotPath is hack_wire.go's hackScreenshotPath, kept here
// for hack_mode.go's event-host routing, which needs it to recognize
// /screenshots/<team> before dispatching to the Hack-side hook.
func hackSitesScreenshotPath(p string) (string, bool) {
	const prefix = "/screenshots/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	slug := p[len(prefix):]
	if !hackSitesGallerySlug(slug) {
		return "", false
	}
	return slug, true
}

// hackSitesPaperUser is hack.go's paperHackUser (moved there from
// hack_paper_metrics.go), duplicated here for site_storage.go's
// storage-request telemetry, which is not Hack-event-API code.
func hackSitesPaperUser(r *http.Request, user *db.User) {
	if user == nil {
		return
	}
	member := user.ID
	if user.Team != nil {
		member = user.Team.MemberID
	}
	if user.EventWebsite != nil {
		member = user.EventWebsite.OrganiserID
	}
	paperRecorder(r.Context()).HackMember(r.Context(), member)
}
