package core

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

type SweepReport struct{ Ctx context.Context }

func (r SweepReport) Progress() { sites.ReportWorkerProgress(r.Ctx) }

func (r SweepReport) Fail(op string, err error) { sites.ReportWorkerFailure(r.Ctx, op, err) }

func GroupHostTexts() web.GroupTexts {
	return web.GroupTexts{
		NotFoundTitle:   "Event not found",
		NotFoundBody:    "No event or page lives at this address.",
		NoPersonalSites: "team sites belong to an event: select your team in the Simple Hack connection before publishing",
	}
}

// OrganiserWrite is loadMember for an organiser change to an event that has
// not ended.
func OrganiserWrite(w http.ResponseWriter, r *http.Request, svc *members.Service) (Access, bool) {
	a, ok := LoadMember(w, r, svc, r.PathValue("slug"), true, "organiser")
	if ok && a.Event.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return a, false
	}
	return a, ok
}

func WriteCSV(w http.ResponseWriter, rows [][]string) {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = httpx.CSVSafe(row[i])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
}

func EntryImageAllowed(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

func (h *Hack) ServeTeamScreenshot(w http.ResponseWriter, r *http.Request, teamID string) {
	data, mime, err := h.Entries.Screenshot(r.Context(), teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
			return
		}
		log.Printf("hack: screenshot: %v", err)
		httpx.WriteInternal(w)
		return
	}
	if len(data) == 0 || !EntryImageAllowed(mime) {
		httpx.WriteError(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func PaperTimes(r *http.Request) { paper(r.Context()).HackPhasesChanged(r.Context()) }

func (h *Hack) TeamOrganiserFrom(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam, tl TeamList) (map[string]any, error) {
	obj := TeamOrganiserJSONFrom(team, tl.Members[team.ID], h.Links.TeamLinkURL(team.Code))
	track, err := h.Teams.Track(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	obj["track"] = track
	obj["site"] = h.Links.TeamSiteJSONFrom(ev, team, tl.Site(team.Slug), tl.Ready)
	obj["deadline"], obj["frozen"], obj["extended"], obj["pinned_version"] = nil, false, false, nil
	if st, ok := tl.States[team.ID]; ok {
		teamStateJSON(st, obj)
	}
	obj["deadline_override"] = RFC3339(team.DeadlineOverride)
	obj["pinned_url"] = nil
	if v, ok := obj["pinned_version"].(int64); ok {
		if u, ok := h.Links.pinnedLink(ev, team, tl, int(v)); ok {
			obj["pinned_url"] = u
		}
	}
	return obj, nil
}

func (h *Hack) DeliverNoticeMail(ctx context.Context, mailer events.Mailer) (int64, error) {
	sender, ok := NoticeSender(mailer)
	if !ok {
		return 0, nil
	}
	delay, window := events.NoticeMailTiming()
	return h.Events.DeliverNoticeMail(ctx, sender, delay, window, h.Links.PublicBaseURL, h.Entries)
}

// NoticeSender is the mailer as a notice sender, when it is one and is
// ready to send.
func NoticeSender(m events.Mailer) (interface {
	SendNotice(string, string, string) error
}, bool) {
	sender, ok := m.(interface {
		SendNotice(string, string, string) error
	})
	if ready, can := m.(interface{ CanSendNotice() bool }); can && !ready.CanSendNotice() {
		return nil, false
	}
	return sender, ok
}

// FixedScheduleFor is the fixed rows for one reader: the organiser (with
// prompts), a participant (with their team's own deadline) or the public.
func (h *Hack) FixedScheduleFor(ctx context.Context, ev hackmodel.Event, member *hackmodel.EventMember, organiser bool) ([]FixedRow, error) {
	in := FixedInputs{Event: ev, Organiser: organiser, Now: time.Now()}
	voting, err := h.Entries.VotingSettings(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	in.Voting = &voting
	if in.Results, err = h.Entries.EventResults(ctx, ev.ID); err != nil {
		return nil, err
	}
	if member != nil && member.Role == "participant" && member.TeamID.Valid {
		if st, err := h.Teams.TeamWriteStateForLookup(ctx, member.EventID, member.TeamID.String, false); err == nil {
			in.Team = &st
		}
	}
	return FixedSchedule(in), nil
}

// PublicSchedule drops the organiser's prompts from editable rows for
// everyone else.
func PublicSchedule(items []events.ScheduleItem) []events.ScheduleItem {
	out := make([]events.ScheduleItem, len(items))
	for i, it := range items {
		it.Prompt = ""
		out[i] = it
	}
	return out
}

func TeamOrganiserJSONFrom(team hackmodel.EventTeam, people []hackmodel.EventPerson, link string) map[string]any {
	members := make([]map[string]any, 0, len(people))
	for _, p := range people {
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"email":        p.Email,
			"display_name": p.DisplayName,
			"admin":        p.Admin,
		})
	}
	return map[string]any{
		"id":         team.ID,
		"slug":       team.Slug,
		"name":       team.Name,
		"code":       team.Code,
		"link":       link,
		"locked":     team.Locked(),
		"kept":       team.KeptAfterLeave,
		"created_at": RFC3339Time(team.CreatedAt),
		"members":    members,
	}
}

func GroupRef(ev hackmodel.Event) web.GroupRef {
	return web.GroupRef{ID: ev.ID, Slug: ev.Slug, AccountID: ev.AccountID}
}

// DeliverContentMail sends the queued announcement mail through mailer (a no-op when it cannot send notices).
func (h *Hack) DeliverContentMail(ctx context.Context, mailer events.Mailer) (int64, error) {
	sender, ok := mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return 0, nil
	}
	return h.Events.DeliverContentMail(ctx, sender, h.Links.SiteDomain, h.Links.PublicBaseURL)
}
