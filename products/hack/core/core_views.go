package core

import (
	"context"
	"log"
	"strings"
	"time"

	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// eventView is GET /v1/hack/events/{slug}'s body for one member: the event,
// their role, abilities and own view, what waits on them, the checklist,
// and the organiser's or participant's extra blocks.
func (h *Hack) EventView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember, organiser bool) map[string]any {
	resultState, _ := h.Entries.EventResults(ctx, ev.ID)
	me := h.meView(ctx, ev, member)
	body := map[string]any{
		"event": h.eventJSON(ctx, ev, resultState),
		"role":  member.Role,
		"can":   h.Events.MemberAbilities(ctx, ev.ID, member),
		"me":    me,
		// What waits on this person here (hack_notices.go): the sidebar
		// badges, Overview's Needs you card and hack_get_team_status.
		"needs_you": h.NeedsYou(ctx, ev, member),
	}
	if organiser {
		participants := h.organiserJSON(ctx, ev, body)
		step := nextStepJSON(ev, resultState, participants)
		// cue: a schedule row lining up with this move has come.
		step["cue"] = h.scheduleCue(ctx, ev, step)
		body["next_step"] = step
	}
	if member.Role == "participant" {
		h.participantJSON(ctx, ev, member, me, body)
	}
	body["checklist"] = h.checklistForView(ctx, ev, member)
	return body
}

func (h *Hack) eventJSON(ctx context.Context, ev hackmodel.Event, results entries.EventResultsRow) map[string]any {
	tracks, _ := h.Events.EventTracks(ctx, ev.ID)
	out := map[string]any{
		"slug":              ev.Slug,
		"title":             ev.Title,
		"tagline":           ev.Tagline,
		"about":             ev.About,
		"rules":             ev.Rules,
		"prizes":            ev.Prizes,
		"overall_prizes":    append([]string{}, ev.OverallPrizes...),
		"coc_text":          ev.CocText,
		"coc_default":       DefaultCoC,
		"stage":             ev.Stage,
		"time_zone":         ev.TimeZone,
		"starts_at":         RFC3339(ev.StartsAt),
		"ends_at":           RFC3339(ev.EndsAt),
		"team_size_max":     ev.TeamSizeMax,
		"team_finding":      ev.TeamFinding,
		"applications_open": ev.ApplicationsOpen,
		"url":               h.Links.EventURL(ev.Slug),
		"builtin_url":       h.Links.PublicBaseURL + "/e/" + ev.Slug,
		"website_mode":      ev.WebsiteMode,
		"icon_url":          h.Links.IconURL(ev),
		"accent_color":      ev.AccentColor, "color": EventAccent(ev),
		"theme":                ev.Theme,
		"icon_media_type":      ev.IconMediaType,
		"taken_down":           ev.TakenDown(),
		"results_visibility":   ev.ResultsVisibility,
		"submission_deadline":  RFC3339(ev.SubmissionDeadline),
		"judging_ends_at":      RFC3339(ev.JudgingEndsAt),
		"entry_required":       EntryRequiredList(ev.EntryRequired),
		"gallery_open":         ev.GalleryOpen,
		"submissions_public":   ev.SubmissionsPublic,
		"team_sites_ready":     h.Links.TeamSitesReady(ev.Slug),
		"stages_offered":       StagesOffered,
		"judging_locked_at":    RFC3339(ev.JudgingLockedAt),
		"scoring_open":         h.ScoringStage(ctx, ev),
		"results_published":    results.Published,
		"results_published_at": RFC3339(results.PublishedAt),
		"judging_lock_reason":  ev.JudgingLockReason,
		"tracks":               tracks,
	}
	// Newest first, for the participant's client-side unread badge on Announcements: no read state is kept on the server.
	if times, err := h.Events.AnnouncementTimes(ctx, ev.ID); err == nil {
		out["announcement_times"] = times
	}
	return out
}

func (h *Hack) organiserJSON(ctx context.Context, ev hackmodel.Event, body map[string]any) int {
	participants, teams, _, _ := h.Events.CountEventMembersLookup(ctx, ev)
	judges, _ := h.Events.CountEligibleJudgesLookup(ctx, ev)
	onNone, _ := h.Events.CountOnNoTeamLookup(ctx, ev.ID)
	emailable, _ := h.Events.CountEmailableParticipantsLookup(ctx, ev.ID)
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
		"join_url":              h.Links.JoinURL(ev.JoinCode),
		"judge_code":            ev.JudgeCode,
		"judge_url":             h.Links.JudgeURL(ev.JudgeCode),
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

// participantJSON adds a participant's approval status to me and, once they have a team, the Home checklist to body.
func (h *Hack) participantJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember, me, body map[string]any) {
	if member.ApprovalStatus != "" {
		me["approval_status"] = member.ApprovalStatus
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

func (h *Hack) memberProgressJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) (map[string]any, error) {
	team, err := h.Teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	st, err := h.Teams.TeamWriteStateForLookup(ctx, team.EventID, team.ID, false)
	if err != nil {
		return nil, err
	}
	return h.TeamProgressJSON(ctx, ev, team, member.UserID, st)
}

// nextStepJSON is the Overview's one forward move; the server computes it so the website and AI apps agree.
func nextStepJSON(ev hackmodel.Event, results entries.EventResultsRow, participants int) map[string]any {
	step := func(stage, label string, deadlinePassed bool, blocked any) map[string]any {
		return map[string]any{"kind": "stage", "stage": stage, "label": label, "deadline_passed": deadlinePassed, "blocked": blocked}
	}
	switch ev.Stage {
	case "draft":
		// A new event hasn't had its entry rules chosen yet (it already carries the default preset's fields, so teams could fill an entry before this; EntryRulesChosen only gates this card).
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

func (h *Hack) meView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) map[string]any {
	me := map[string]any{
		// user_id: the signed-in person's own ID, same value the people list already
		// shows for everyone else on this event -- so exposing it here reveals
		// nothing a member couldn't already read about themselves.
		"user_id":         member.UserID,
		"display_name":    member.DisplayName,
		"coc_accepted_at": RFC3339(member.CocAcceptedAt),
		"team":            nil,
	}
	if member.Role != "participant" {
		return me
	}
	if !member.TeamID.Valid {
		var notice string
		if err := h.Teams.TeamRemovalNotice(ctx, ev.ID, member.UserID, &notice); err == nil {
			me["team_removal_notice"] = notice
		}
		return me
	}
	if t, err := h.myTeamJSON(ctx, ev, member); err == nil {
		me["team"] = t
	}
	return me
}

func (h *Hack) myTeamJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) (map[string]any, error) {
	team, err := h.Teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	people, err := h.Teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	members, youAdmin, admins := teamPeopleJSON(people, member.UserID)
	var lookingFor string
	_ = h.Teams.LookingFor(ctx, team.ID, &lookingFor)
	t := map[string]any{
		"slug":        team.Slug,
		"name":        team.Name,
		"code":        team.Code,
		"link":        h.Links.TeamLinkURL(team.Code),
		"admin":       youAdmin,
		"locked":      team.Locked(),
		"kept":        team.KeptAfterLeave,
		"looking_for": lookingFor,
		"members":     members,
		"site":        h.Links.teamSiteJSON(ctx, ev, team),
	}
	if youAdmin {
		h.teamAdminJSON(ctx, team.ID, member.UserID, admins == 1, t)
	}
	if track, err := h.Events.TeamTrackLookup(ctx, team.ID); err == nil {
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
func (h *Hack) teamAdminJSON(ctx context.Context, teamID, userID string, onlyAdmin bool, t map[string]any) {
	if onlyAdmin {
		var successor string
		if err := h.Teams.AdminSuccessor(ctx, teamID, userID, &successor); err == nil {
			t["admin_successor"] = successor
		}
	}
	var pending int
	_ = h.Teams.PendingRequests(ctx, teamID, &pending)
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

func EntryRequiredList(in []string) []string {
	set := map[string]bool{}
	for _, f := range in {
		set[f] = true
	}
	return keysInOrder(set)
}

func (h *Hack) ChecklistService() *events.ChecklistService {
	return events.NewChecklist(h.DB, h.Entries, ChecklistTargets{h.Links})
}

func (h *Hack) ReadChecklist(ctx context.Context, ev hackmodel.Event, m hackmodel.EventMember, role string) (events.Checklist, error) {
	var forward *events.ChecklistNext
	if m.Role == "organiser" && role != "judge" {
		result, err := h.Entries.EventResults(ctx, ev.ID)
		if err != nil {
			return events.Checklist{}, err
		}
		participants, _, _, err := h.Events.CountEventMembersLookup(ctx, ev)
		if err != nil {
			return events.Checklist{}, err
		}
		step := nextStepJSON(ev, result, participants)
		if kind, _ := step["kind"].(string); kind != "none" {
			stage, _ := step["stage"].(string)
			label, _ := step["label"].(string)
			passed, _ := step["deadline_passed"].(bool)
			forward = &events.ChecklistNext{Kind: kind, Stage: stage, Label: label, ConfirmationRequired: true, BlockedReason: step["blocked"], DeadlinePassed: passed, Cue: h.scheduleCue(ctx, ev, step)}
		}
	}
	return h.ChecklistService().Read(ctx, ev.ID, m.UserID, role, m.JoinedAt.IsZero(), forward, time.Now())
}

func (h *Hack) checklistForView(ctx context.Context, ev hackmodel.Event, m hackmodel.EventMember) any {
	list, err := h.ReadChecklist(ctx, ev, m, "")
	if err != nil {
		log.Printf("hack: checklist %s: %v", ev.Slug, err)
		return nil
	}
	return list
}

type ChecklistTargets struct{ Links *Links }

func (h *Hack) TeamProgressJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam, userID string, st hackmodel.TeamWriteState) (map[string]any, error) {
	people, err := h.Teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	var teammates []string
	for _, p := range people {
		if p.UserID != userID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	return h.entryProgressJSON(ctx, ev, team, teammates, st)
}

func (h *Hack) entryProgressJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam, teammates []string, st hackmodel.TeamWriteState) (map[string]any, error) {
	p, err := h.Entries.EntryProgress(ctx, ev, team, teammates, st)
	if err != nil {
		return nil, err
	}
	e := p.Entry
	steps := []map[string]any{
		{"id": "team", "done": true, "detail": p.TeamLine},
		{"id": "summary", "done": p.Filled >= p.Of, "filled": p.Filled, "of": p.Of, "missing": p.SummaryMissing},
		{"id": "evidence", "done": p.EvidenceDone, "summary": p.Summary, "missing": p.EvidenceMissing},
		{"id": "submit", "done": e.Status() == "submitted", "status": e.Status(), "submitted_at": RFC3339(e.SubmittedAt), "auto": e.SubmitAuto},
	}
	return map[string]any{"steps": steps, "next": p.Next, "deadline": RFC3339(st.Deadline), "deadline_is_extension": st.HasOverride, "event_deadline": RFC3339(st.EventDeadline), "server_now": RFC3339Time(p.Now)}, nil
}

// scoringStage reports whether judges may score in the event's stage:
// during Judging, or while building on the standing demo
// (including its weekly replacement slug) so visitors can try judging.
func (h *Hack) ScoringStage(ctx context.Context, ev hackmodel.Event) bool {
	if ev.Stage == "judging" {
		return true
	}
	if h.DemoSlug == "" || (ev.Slug != h.DemoSlug && !strings.HasPrefix(ev.Slug, h.DemoSlug+"-")) {
		return false
	}
	demo, err := h.Events.DemoEvent(ctx, h.DemoSlug)
	return err == nil && demo.ID == ev.ID
}

func (h *Hack) NeedsYou(ctx context.Context, ev hackmodel.Event, m hackmodel.EventMember) []map[string]any {
	return NeedsYouView(h.Events.NeedsYou(ctx, ev, m, h.EntryEvaluator()))
}

// entryEvaluator is h.entries as a true nil events.EntryEvaluator when
// h.entries itself is nil (a bare &HackHandler{} test fixture): a nil
// *entries.Service boxed in a non-nil interface would panic on first call,
// not compare equal to nil.
func (h *Hack) EntryEvaluator() events.EntryEvaluator {
	if h.Entries == nil {
		return nil
	}
	return h.Entries
}

func NeedsYouView(needs []events.Need) []map[string]any {
	items := make([]map[string]any, 0, len(needs))
	for _, n := range needs {
		item := map[string]any{"kind": n.Kind, "id": nil, "count": n.Count, "text": n.Text, "section": n.Section, "at": nil, "dismissable": n.ID != ""}
		if n.ID != "" {
			item["id"] = n.ID
		}
		if n.Team != "" {
			item["team"] = n.Team
		}
		if !n.At.IsZero() {
			item["at"] = n.At.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items
}

func VotingStatus(ev hackmodel.Event, s hackmodel.VotingSettings, now time.Time) string {
	return entries.VotingStatus(ev, entries.VotingSettingsValue(s.Enabled, s.OpensAt, s.ClosesAt, s.Eligibility), now)
}

func (t ChecklistTargets) Resolve(id, slug string, params map[string]string) (events.Link, bool) {
	params["event_url"] = t.Links.EventURL(slug)
	return events.ChecklistTargets{}.Resolve(id, slug, params)
}
