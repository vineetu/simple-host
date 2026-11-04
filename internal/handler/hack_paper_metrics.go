package handler

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
)

// paperHackEvent, paperHackUser, paperHackKnownMember and paperHackStage
// moved to hack.go, and paperHackTimes to hack_judging.go (cleanup 3.2 slice
// 1c): free functions with no *SiteHandler dependency, called only from the
// event API's own moving files. paperHackSite, paperHackDeploy,
// paperHackTeamState and paperHackWrite below stay: the first two are
// *SiteHandler methods, and the latter two are called only from the
// (staying) storage/deploy side.

func (h *SiteHandler) paperHackSite(r *http.Request, account, site string, user *model.User) {
	if !h.ed.Identity().PaperMetrics {
		return
	}
	member := ""
	if user != nil {
		member = user.ID
		if user.Team != nil {
			member = user.Team.MemberID
		}
		if user.EventWebsite != nil {
			member = user.EventWebsite.OrganiserID
		}
	}
	p := r.URL.Path
	write := r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" &&
		(strings.Contains(p, "/storage/kv/") || strings.Contains(p, "/storage/sqlite/") || strings.Contains(p, "/storage/files/")) &&
		!strings.HasSuffix(p, "/query") && !strings.HasSuffix(p, "/download-link")
	paperRecorder(r.Context()).ResolveHackSite(r.Context(), account, site, member, write)
}

func (h *SiteHandler) paperHackDeploy(r *http.Request, user *model.User) {
	if !h.ed.Identity().PaperMetrics || user == nil || user.Team == nil {
		return
	}
	paperRecorder(r.Context()).HackDeploy(r.Context())
}

func paperHackTeamState(r *http.Request, user *model.User, stage string) {
	if user != nil && user.Team != nil {
		paperRecorder(r.Context()).ResolveHackEvent(r.Context(), user.Team.EventSlug, stage, user.Team.MemberID)
	}
}

func paperHackWrite(r *http.Request) { paperRecorder(r.Context()).HackWriteCommitted(r.Context()) }
