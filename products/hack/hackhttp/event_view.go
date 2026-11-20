package hackhttp

import (
	"context"
	"log"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *hackBase) eventView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember, organiser bool) map[string]any {
	// The code of conduct is the default text plus the organiser's own.
	cocText := ev.CocText
	tracks, _ := h.events.EventTracks(ctx, ev.ID)
	resultState, _ := h.entries.EventResults(ctx, ev.ID)
	body := map[string]any{
		"event": map[string]any{
			"slug":              ev.Slug,
			"title":             ev.Title,
			"tagline":           ev.Tagline,
			"about":             ev.About,
			"rules":             ev.Rules,
			"prizes":            ev.Prizes,
			"overall_prizes":    append([]string{}, ev.OverallPrizes...),
			"coc_text":          cocText,
			"coc_default":       HackDefaultCoC,
			"stage":             ev.Stage,
			"time_zone":         ev.TimeZone,
			"starts_at":         rfc3339UTC(ev.StartsAt),
			"ends_at":           rfc3339UTC(ev.EndsAt),
			"team_size_max":     ev.TeamSizeMax,
			"team_finding":      ev.TeamFinding,
			"applications_open": ev.ApplicationsOpen,
			"url":               h.EventURL(ev.Slug),
			"builtin_url":       h.publicBaseURL + "/e/" + ev.Slug,
			"website_mode":      ev.WebsiteMode,
			"icon_url":          h.iconURL(ev),
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
			"team_sites_ready":     h.teamSitesReady(ev.Slug),
			"stages_offered":       hackStagesOfferedList,
			"judging_locked_at":    rfc3339UTC(ev.JudgingLockedAt),
			"scoring_open":         h.scoringStage(ctx, ev),
			"results_published":    resultState.Published,
			"results_published_at": rfc3339UTC(resultState.PublishedAt),
			"judging_lock_reason":  ev.JudgingLockReason,
			"tracks":               tracks,
		},
		"role": member.Role,
		"can":  h.events.MemberAbilities(ctx, ev.ID, member),
		"me":   h.meView(ctx, ev, member),
		// What waits on this person here (hack_notices.go): the sidebar
		// badges, Overview's Needs you card and hack_get_team_status.
		"needs_you": h.needsYou(ctx, ev, member),
	}
	// Newest first, for the participant's client-side unread badge on
	// Announcements (audit 2026-11-02): no read state is kept on the server.
	if times, err := h.events.AnnouncementTimes(ctx, ev.ID); err == nil {
		body["event"].(map[string]any)["announcement_times"] = times
	}
	if organiser {
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
			"join_url":              h.joinURL(ev.JoinCode),
			"judge_code":            ev.JudgeCode,
			"judge_url":             h.judgeURL(ev.JudgeCode),
			"counts": map[string]int{
				"participants":           participants,
				"teams":                  teams,
				"judges":                 judges,
				"on_no_team":             onNone,
				"emailable_participants": emailable,
			},
		}
		step := nextStepJSON(ev, resultState, participants)
		// cue: a schedule row lining up with this move has come (owner,
		// 2026-11-02). It only words the card. The move still waits for the
		// organiser's press.
		step["cue"] = h.scheduleCue(ctx, ev, step)
		body["next_step"] = step
	}
	if member.Role == "participant" {
		if status, err := h.members.MemberApprovalStatusLookup(ctx, ev.ID, member.UserID); err == nil {
			body["me"].(map[string]any)["approval_status"] = status
		}
		// progress (section 4.3): the Home checklist, only once the person
		// has a team -- there is nothing to track before that.
		if member.TeamID.Valid {
			if progress, err := h.memberProgressJSON(ctx, ev, member); err == nil {
				body["progress"] = progress
			} else {
				log.Printf("hack: progress %s: %v", ev.Slug, err)
			}
		}
	}
	body["checklist"] = h.checklistForView(ctx, ev, member)
	return body
}

// memberProgressJSON is eventView's "progress" for a team's participant:
// entryProgressJSON with the team and teammates loaded.
func (h *hackBase) memberProgressJSON(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) (map[string]any, error) {
	team, err := h.teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	st, err := h.teams.TeamWriteStateForLookup(ctx, team.ID, false)
	if err != nil {
		return nil, err
	}
	people, err := h.teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	var teammates []string
	for _, p := range people {
		if p.UserID != member.UserID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	return h.entryProgressJSON(ctx, ev, team, teammates, st)
}

// nextStepJSON is the Overview's one forward move: the server computes it so
// the website and AI apps agree (parity rule 2026-10-27). kind is "stage"
// (set stage to the given stage), "entry_rules" (SIM-382: open the "What
// teams hand in" setting on Entries; no stage change), "publish_results"
// (POST .../results/publish {}) or "none" (archived: no button). blocked is
// nil or "archive_needs_participants" (End with nobody in the event): the
// button still shows, disabled, pointing to Settings -> Delete. eventView
// adds cue (hackScheduleCue): the schedule row that lines up with this move
// once its time has come, or nil.
func nextStepJSON(ev hackmodel.Event, results entries.EventResultsRow, participants int) map[string]any {
	step := func(stage, label string, deadlinePassed bool, blocked any) map[string]any {
		return map[string]any{"kind": "stage", "stage": stage, "label": label, "deadline_passed": deadlinePassed, "blocked": blocked}
	}
	switch ev.Stage {
	case "draft":
		// SIM-382: a new event hasn't had its entry rules chosen yet (it
		// already carries the default preset's fields, so teams could fill
		// an entry before this -- EntryRulesChosen only gates this card).
		// Once the organiser saves a choice, even the unchanged default,
		// this step moves on to Open sign-up. An event that already had
		// rules before this column existed starts out chosen (the
		// migration backfills it true), so it never sees this step.
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

func (h *hackBase) meView(ctx context.Context, ev hackmodel.Event, member hackmodel.EventMember) map[string]any {
	me := map[string]any{
		// user_id: the signed-in person's own ID, same value the people
		// list already shows for everyone else on this event -- so
		// exposing it here reveals nothing a member couldn't already read
		// about themselves. Added for the MCP organiser app's own "(you)"
		// labels (hack_apps_organiser.go's hackShowOrganiserViewOnly),
		// which need to compare an event member's user_id against the
		// caller's own without a dedicated identity tool.
		"user_id":         member.UserID,
		"display_name":    member.DisplayName,
		"coc_accepted_at": rfc3339UTC(member.CocAcceptedAt),
		"team":            nil,
	}
	if member.Role != "participant" || !member.TeamID.Valid {
		if member.Role == "participant" {
			var notice string
			if err := h.teams.TeamRemovalNotice(ctx, ev.ID, member.UserID, &notice); err == nil {
				me["team_removal_notice"] = notice
			}
		}
		return me
	}
	team, err := h.teams.TeamByID(ctx, member.TeamID.String)
	if err != nil {
		return me
	}
	people, err := h.teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return me
	}
	members := make([]map[string]any, 0, len(people))
	var youAdmin bool
	admins := 0
	for _, p := range people {
		you := p.UserID == member.UserID
		if p.Admin {
			admins++
			if you {
				youAdmin = true
			}
		}
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"display_name": p.DisplayName,
			"you":          you,
			"admin":        p.Admin,
		})
	}
	var lookingFor string
	_ = h.teams.LookingFor(ctx, team.ID, &lookingFor)
	t := map[string]any{
		"slug":        team.Slug,
		"name":        team.Name,
		"code":        team.Code,
		"link":        h.teamLinkURL(team.Code),
		"admin":       youAdmin,
		"locked":      team.Locked(),
		"kept":        team.KeptAfterLeave,
		"looking_for": lookingFor,
		"members":     members,
		"site":        h.teamSiteJSON(ctx, ev, team),
	}
	// admin_successor: shown only when the caller is the team's one admin and
	// others remain, so they know who takes over if they leave or step down
	// (EnsureTeamAdmin's own tie-break: longest-standing, then user_id;
	// NULLS LAST, so a row with no team_joined_at never jumps the queue --
	// Grok security review, 2026-11-01).
	if youAdmin && admins == 1 {
		var successor string
		if err := h.teams.AdminSuccessor(ctx, team.ID, member.UserID, &successor); err == nil {
			t["admin_successor"] = successor
		}
	}
	if youAdmin {
		var pending int
		_ = h.teams.PendingRequests(ctx, team.ID, &pending)
		t["pending_requests"] = pending
	}
	if track, err := h.events.TeamTrackLookup(ctx, team.ID); err == nil {
		t["track"] = track
	}
	h.teamDeadlineJSON(ctx, team, t)
	me["team"] = t
	return me
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
