package handler

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/papermetrics"
)

// These hooks annotate the outer paper collector using resolutions the handler
// already performed. With collection off they are context lookups only.
func paperHackEvent(r *http.Request, ev db.Event) {
	papermetrics.ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, "", ev.ResultsPublishedAt.Time)
	paperHackUser(r, auth.GetUser(r.Context()))
}

func paperHackUser(r *http.Request, user *db.User) {
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
	papermetrics.HackMember(r.Context(), member)
}

func paperHackSite(r *http.Request, account, site string, user *db.User) {
	if !hackMode {
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
	papermetrics.ResolveHackSite(r.Context(), account, site, member, write)
}

func paperHackDeploy(r *http.Request, user *db.User) {
	if !hackMode || user == nil || user.Team == nil {
		return
	}
	papermetrics.HackDeploy(r.Context())
}

func paperHackKnownMember(r *http.Request, ev db.Event, member string) {
	papermetrics.ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, member, ev.ResultsPublishedAt.Time)
}

func paperHackTeamState(r *http.Request, user *db.User, stage string) {
	if user != nil && user.Team != nil {
		papermetrics.ResolveHackEvent(r.Context(), user.Team.EventSlug, stage, user.Team.MemberID)
	}
}

func paperHackStage(r *http.Request, ev db.Event) {
	papermetrics.RecordHackStage(r.Context(), ev.ID, ev.Stage, ev.UpdatedAt)
}

func paperHackTimes(r *http.Request) { papermetrics.HackPhasesChanged(r.Context()) }

func paperHackWrite(r *http.Request) { papermetrics.HackWriteCommitted(r.Context()) }
