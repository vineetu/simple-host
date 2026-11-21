package hackhttp

import (
	"context"
	"log"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// eventView is GET /v1/hack/events/{slug}'s body for one member: the event,
// their role, abilities and own view, what waits on them, the checklist,
// and the organiser's or participant's extra blocks.
func (h *views) eventView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember, organiser bool) map[string]any {
	resultState, _ := h.entries.EventResults(ctx, ev.ID)
	me := h.meView(ctx, ev, member)
	body := map[string]any{
		"event": h.eventJSON(ctx, ev, resultState),
		"role":  member.Role,
		"can":   h.events.MemberAbilities(ctx, ev.ID, member),
		"me":    me,
		// What waits on this person here (hack_notices.go): the sidebar
		// badges, Overview's Needs you card and hack_get_team_status.
		"needs_you": h.needsYou(ctx, ev, member),
	}
	if organiser {
		participants := h.organiserJSON(ctx, ev, body)
		step := nextStepJSON(ev, resultState, participants)
		// cue: a schedule row lining up with this move has come (owner, 2026-11-02).
		step["cue"] = h.scheduleCue(ctx, ev, step)
		body["next_step"] = step
	}
	if member.Role == "participant" {
		h.participantJSON(ctx, ev, member, me, body)
	}
	body["checklist"] = h.checklistForView(ctx, ev, member)
	return body
}

// eventJSON is the event block every member reads.
func (h *views) eventJSON(ctx context.Context, ev hackmodel.Event, results entries.EventResultsRow) map[string]any {
	tracks, _ := h.events.EventTracks(ctx, ev.ID)
	out := map[string]any{
		"slug":              ev.Slug,
		"title":             ev.Title,
		"tagline":           ev.Tagline,
		"about":             ev.About,
		"rules":             ev.Rules,
		"prizes":            ev.Prizes,
		"overall_prizes":    append([]string{}, ev.OverallPrizes...),
		"coc_text":          ev.CocText,
		"coc_default":       HackDefaultCoC,
		"stage":             ev.Stage,
		"time_zone":         ev.TimeZone,
		"starts_at":         rfc3339UTC(ev.StartsAt),
		"ends_at":           rfc3339UTC(ev.EndsAt),
		"team_size_max":     ev.TeamSizeMax,
		"team_finding":      ev.TeamFinding,
		"applications_open": ev.ApplicationsOpen,
		"url":               h.links.EventURL(ev.Slug),
		"builtin_url":       h.links.publicBaseURL + "/e/" + ev.Slug,
		"website_mode":      ev.WebsiteMode,
		"icon_url":          h.links.iconURL(ev),
		"accent_color":      ev.AccentColor, "color": hackEventAccent(ev),
		"theme":                ev.Theme,
		"icon_media_type":      ev.IconMediaType,
		"taken_down":           ev.TakenDown(),
		"results_visibility":   ev.ResultsVisibility,
		"submission_deadline":  rfc3339UTC(ev.SubmissionDeadline),
		"judging_ends_at":      rfc3339UTC(ev.JudgingEndsAt),
		"entry_required":       entryRequiredList(ev.EntryRequired),
		"gallery_open":         ev.GalleryOpen,
		"submissions_public":   ev.SubmissionsPublic,
		"team_sites_ready":     h.links.teamSitesReady(ev.Slug),
		"stages_offered":       hackStagesOfferedList,
		"judging_locked_at":    rfc3339UTC(ev.JudgingLockedAt),
		"scoring_open":         h.scoringStage(ctx, ev),
		"results_published":    results.Published,
		"results_published_at": rfc3339UTC(results.PublishedAt),
		"judging_lock_reason":  ev.JudgingLockReason,
		"tracks":               tracks,
	}
	// Newest first, for the participant's client-side unread badge on
	// Announcements (audit 2026-11-02): no read state is kept on the server.
	if times, err := h.events.AnnouncementTimes(ctx, ev.ID); err == nil {
		out["announcement_times"] = times
	}
	return out
}

// organiserJSON adds the organiser block (codes, links and counts) to body
// and answers the participant count.
func (h *views) organiserJSON(ctx context.Context, ev hackmodel.Event, body map[string]any) int {
	participants, teams, _, _ := h.events.CountEventMembersLookup(ctx, ev.ID)
	judges, _ := h.events.CountEligibleJudgesLookup(ctx, ev.ID)
	onNone, _ := h.events.CountOnNoTeamLookup(ctx, ev.ID)
	emailable, _ := h.events.CountEmailableParticipantsLookup(ctx, ev.ID)
	exp := any(nil)
	if ev.ExpectedParticipants.Valid {
		exp = ev.ExpectedParticipants.Int64
	}
	body["organiser"] = map[string]any{
		// The event website's site name: publish it with the site tools,
		// owner set to the event's name.
		"website_site":          ev.ID,
		"organiser_name":        ev.OrganiserName,
		"organisation":          ev.Organisation,
		"contact_email":         ev.ContactEmail,
		"purpose":               ev.Purpose,
		"expected_participants": exp,
		"join_code":             ev.JoinCode,
		"join_url":              h.links.joinURL(ev.JoinCode),
		"judge_code":            ev.JudgeCode,
		"judge_url":             h.links.judgeURL(ev.JudgeCode),
		"counts": map[string]int{
			"participants":           participants,
			"teams":                  teams,
			"judges":                 judges,
			"on_no_team":             onNone,
			"emailable_participants": emailable,
		},
	}
	return participants
}

// participantJSON adds a participant's approval status to me and, once they
// have a team, the Home checklist (section 4.3) to body.
func (h *views) participantJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember, me, body map[string]any) {
	if status, err := h.members.MemberApprovalStatusLookup(ctx, ev.ID, member.UserID); err == nil {
		me["approval_status"] = status
	}
	if !member.TeamID.Valid {
		return
	}
	progress, err := h.memberProgressJSON(ctx, ev, member)
	if err != nil {
		log.Printf("hack: progress %s: %v", ev.Slug, err)
		return
	}
	body["progress"] = progress
}

// memberProgressJSON is eventView's "progress" for a team's participant:
// teamProgressJSON with the team and its write state loaded.
func (h *views) memberProgressJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) (map[string]any, error) {
	team, err := h.teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	st, err := h.teams.TeamWriteStateForLookup(ctx, team.ID, false)
	if err != nil {
		return nil, err
	}
	return h.teamProgressJSON(ctx, ev, team, member.UserID, st)
}

// nextStepJSON is the Overview's one forward move: the server computes it so
// the website and AI apps agree (parity rule 2026-10-27).
func nextStepJSON(ev hackmodel.Event, results entries.EventResultsRow, participants int) map[string]any {
	step := func(stage, label string, deadlinePassed bool, blocked any) map[string]any {
		return map[string]any{"kind": "stage", "stage": stage, "label": label, "deadline_passed": deadlinePassed, "blocked": blocked}
	}
	switch ev.Stage {
	case "draft":
		// SIM-382: a new event hasn't had its entry rules chosen yet (it already
		// carries the default preset's fields, so teams could fill an entry before
		// this -- EntryRulesChosen only gates this card).
		if !ev.EntryRulesChosen {
			return map[string]any{"kind": "entry_rules", "stage": "", "label": "Choose what teams hand in", "deadline_passed": false, "blocked": nil}
		}
		return step("open", "Open sign-up", false, nil)
	case "open":
		return step("building", "Start building", false, nil)
	case "building":
		deadlinePassed := ev.SubmissionDeadline.Valid && !ev.SubmissionDeadline.Time.After(time.Now())
		return step("closed", "Close entries", deadlinePassed, nil)
	case "closed":
		return step("judging", "Start judging", false, nil)
	case "judging":
		return step("results", "Finish judging", false, nil)
	case "results":
		if !results.Published {
			return map[string]any{"kind": "publish_results", "stage": "", "label": "Publish results", "deadline_passed": false, "blocked": nil}
		}
		var blocked any
		if participants == 0 {
			blocked = "archive_needs_participants"
		}
		return step("archived", "End event", false, blocked)
	default: // archived
		return map[string]any{"kind": "none", "stage": "", "label": "", "deadline_passed": false, "blocked": nil}
	}
}

// meView is the member's own block: who they are and, for a participant,
// their team or the notice that they were removed from one.
func (h *views) meView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) map[string]any {
	me := map[string]any{
		// user_id: the signed-in person's own ID, same value the people list already
		// shows for everyone else on this event -- so exposing it here reveals
		// nothing a member couldn't already read about themselves.
		"user_id":         member.UserID,
		"display_name":    member.DisplayName,
		"coc_accepted_at": rfc3339UTC(member.CocAcceptedAt),
		"team":            nil,
	}
	if member.Role != "participant" {
		return me
	}
	if !member.TeamID.Valid {
		var notice string
		if err := h.teams.TeamRemovalNotice(ctx, ev.ID, member.UserID, &notice); err == nil {
			me["team_removal_notice"] = notice
		}
		return me
	}
	if t, err := h.myTeamJSON(ctx, ev, member); err == nil {
		me["team"] = t
	}
	return me
}

// myTeamJSON is a participant's own team: its people, link, site, track and
// deadline, plus the admin's successor and pending requests.
func (h *views) myTeamJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) (map[string]any, error) {
	team, err := h.teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	people, err := h.teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	members, youAdmin, admins := teamPeopleJSON(people, member.UserID)
	var lookingFor string
	_ = h.teams.LookingFor(ctx, team.ID, &lookingFor)
	t := map[string]any{
		"slug":        team.Slug,
		"name":        team.Name,
		"code":        team.Code,
		"link":        h.links.teamLinkURL(team.Code),
		"admin":       youAdmin,
		"locked":      team.Locked(),
		"kept":        team.KeptAfterLeave,
		"looking_for": lookingFor,
		"members":     members,
		"site":        h.links.teamSiteJSON(ctx, ev, team),
	}
	if youAdmin {
		h.teamAdminJSON(ctx, team.ID, member.UserID, admins == 1, t)
	}
	if track, err := h.events.TeamTrackLookup(ctx, team.ID); err == nil {
		t["track"] = track
	}
	h.teamDeadlineJSON(ctx, team, t)
	return t, nil
}

// teamPeopleJSON lists the team's people for one of them, and says whether
// that person is an admin and how many admins there are.
func teamPeopleJSON(people []hackmodel.EventPerson, you string) (members []map[string]any, youAdmin bool, admins int) {
	members = make([]map[string]any, 0, len(people))
	for _, p := range people {
		if p.Admin {
			admins++
			youAdmin = youAdmin || p.UserID == you
		}
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"display_name": p.DisplayName,
			"you":          p.UserID == you,
			"admin":        p.Admin,
		})
	}
	return members, youAdmin, admins
}

// teamAdminJSON adds what only a team admin sees: pending requests and,
// when they are the one admin, who takes over if they leave or step down
// (EnsureTeamAdmin's tie-break: longest-standing, then user_id; NULLS LAST,
// so a row with no team_joined_at never jumps the queue).
func (h *views) teamAdminJSON(ctx context.Context, teamID, userID string, onlyAdmin bool, t map[string]any) {
	if onlyAdmin {
		var successor string
		if err := h.teams.AdminSuccessor(ctx, teamID, userID, &successor); err == nil {
			t["admin_successor"] = successor
		}
	}
	var pending int
	_ = h.teams.PendingRequests(ctx, teamID, &pending)
	t["pending_requests"] = pending
}

func keysInOrder(set map[string]bool) []string {
	out := []string{}
	for _, k := range events.EntryFields {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

// entryRequiredList is the required fields in canonical order, never nil.
func entryRequiredList(in []string) []string {
	set := map[string]bool{}
	for _, f := range in {
		set[f] = true
	}
	return keysInOrder(set)
}
