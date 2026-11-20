package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

type Link = rules.ChecklistLink
type ChecklistTargets = rules.ChecklistTargets
type Checklist = rules.Checklist
type ChecklistNext = rules.ChecklistNext

// TargetResolver keeps checklist links independent of the navigation implementation.
type TargetResolver interface {
	Resolve(targetID, slug string, params map[string]string) (Link, bool)
}

// ChecklistService owns checklist fact gathering and explicit writes.
type ChecklistService struct {
	db      *sql.DB
	entries *entries.Service
	targets TargetResolver
}

func NewChecklist(database *sql.DB, entryService *entries.Service, targets TargetResolver) *ChecklistService {
	if targets == nil {
		targets = rules.ChecklistTargets{}
	}
	return &ChecklistService{database, entryService, targets}
}

// Read computes a role-specific checklist. member is always re-read; a missing
// membership is only inspectable when the transport already authorised admin inspection.
func (s *ChecklistService) Read(ctx context.Context, eventID, actorID, role string, adminView bool, forward *rules.ChecklistNext, now time.Time) (Checklist, error) {
	ev, err := eventrepo.GetEventByID(ctx, s.db, eventID)
	if err != nil {
		return Checklist{}, err
	}
	member, err := eventrepo.GetEventMember(ctx, s.db, eventID, actorID)
	readOnly := false
	if errors.Is(err, sql.ErrNoRows) && adminView {
		member = model.EventMember{Role: "organiser"}
		readOnly = true
	} else if err != nil {
		return Checklist{}, err
	}
	if role == "" {
		role = member.Role
	}
	if role != member.Role && !(member.Role == "organiser" && role == "judge" && !readOnly) {
		return Checklist{}, &Error{Code: "checklist_role_forbidden", Message: "this membership cannot read that checklist"}
	}
	if role != "organiser" && role != "judge" && role != "participant" {
		return Checklist{}, &Error{Code: "checklist_role_forbidden", Message: "unknown checklist role"}
	}
	if role == "judge" {
		judge, err := entryrepo.EventJudgeInEvent(ctx, s.db, eventID, actorID)
		if err != nil {
			return Checklist{}, err
		}
		if !judge {
			return Checklist{}, &Error{Code: "checklist_role_forbidden", Message: "only judges can read this checklist"}
		}
	}
	subject := actorID
	if readOnly {
		subject = ""
	}
	marks, acks, err := eventrepo.ChecklistMarks(ctx, s.db, eventID, subject)
	if err != nil {
		return Checklist{}, err
	}
	facts := map[string]rules.ChecklistFact{}
	params := map[string]string{}
	participantNext := ""
	if role == "participant" {
		participantNext, err = s.participantFacts(ctx, ev, member, facts)
		if err != nil {
			return Checklist{}, err
		}
	} else {
		rubric, err := entryrepo.ListRubric(ctx, s.db, eventID)
		if err != nil {
			return Checklist{}, err
		}
		version := rules.RubricFingerprint(rubric)
		var judging entries.ChecklistJudgingFacts
		if role == "organiser" {
			judging, err = s.entries.ChecklistOrganiserJudging(ctx, s.db, ev, rubric)
		} else {
			judging, err = s.entries.ChecklistJudging(ctx, ev, subject, false)
		}
		if err != nil {
			return Checklist{}, err
		}
		if role == "organiser" {
			if err = s.organiserFacts(ctx, ev, rubric, judging, facts); err != nil {
				return Checklist{}, err
			}
			if facts["o.assignments"].TargetID == "o.team-track" {
				for _, t := range judging.Coverage.Short {
					if t.MissingTrack {
						params["team"] = t.Slug
						break
					}
				}
			}
			rubricFact := facts["o.rubric"]
			rubricFact.Version = version
			facts["o.rubric"] = rubricFact
		} else {
			judgeFacts(ev, member, judging, version, acks, facts, params)
		}
	}
	if ev.TakenDown() {
		readOnly = true
		for _, d := range rules.ChecklistCatalogue() {
			f := facts[d.ID]
			f.BlockedReason = "event_taken_down"
			facts[d.ID] = f
		}
	}
	out := rules.BuildChecklist(rules.ChecklistInput{Role: role, Stage: ev.Stage, MembershipRole: member.Role, AssignmentMode: ev.JudgeAssignmentMode, JudgesPerTeam: ev.JudgesPerTeam, ReadOnly: readOnly, Facts: facts, Marks: marks, Forward: forward, ParticipantNext: participantNext}, now)
	for i := range out.Items {
		if link, ok := s.targets.Resolve(out.Items[i].TargetID, ev.Slug, params); ok {
			out.Items[i].Link = &link
		}
	}
	if out.Next != nil && out.Next.TargetID != "" {
		if link, ok := s.targets.Resolve(out.Next.TargetID, ev.Slug, params); ok {
			out.Next.Link = &link
		}
	}
	return out, nil
}

// judgeFacts fills a judge's checklist facts and the queue's first team.
func judgeFacts(ev model.Event, member model.EventMember, judging entries.ChecklistJudgingFacts, version string, acks map[string]string, facts map[string]rules.ChecklistFact, params map[string]string) {
	closed := ev.JudgingLockedAt.Valid || judging.Published || ev.Stage == "results" || ev.Stage == "archived"
	queueOpen := ev.Stage == "judging" && !closed
	facts["j.invite"] = rules.ChecklistFact{Done: !member.JoinedAt.IsZero(), BlockedReason: "informational"}
	facts["j.coc"] = rules.ChecklistFact{Done: member.CocAcceptedAt.Valid, BlockedReason: "conduct_record_only"}
	facts["j.rubric"] = rules.ChecklistFact{Done: (version != "" && acks["j.rubric"] == version) || (judging.OwnTotal > 0 && judging.OwnDone == judging.OwnTotal), Version: version}
	if version == "" && !facts["j.rubric"].Done {
		facts["j.rubric"] = rules.ChecklistFact{Detail: "The organiser hasn't added a rubric yet", BlockedReason: "no_rubric"}
	}
	_, conflictAck := acks["j.conflicts"]
	facts["j.conflicts"] = rules.ChecklistFact{Done: judging.OwnConflicts > 0 || conflictAck}
	if !queueOpen {
		f := facts["j.conflicts"]
		f.BlockedReason = "judging_not_open"
		facts["j.conflicts"] = f
	}
	score := rules.ChecklistFact{Done: judging.OwnTotal > 0 && judging.OwnDone == judging.OwnTotal, Detail: fmt.Sprintf("%d of %d %s scored", judging.OwnDone, judging.OwnTotal, checklistPlural(judging.OwnTotal, "team")), Count: intPointer(judging.OwnDone), Total: intPointer(judging.OwnTotal)}
	if judging.FirstUnfinished == "" {
		score.TargetID = "j.queue"
	}
	if judging.OwnTotal == 0 {
		score.TargetID = "j.queue"
		score.BlockedReason = "nothing_assigned"
		score.Detail = "No teams are in your queue yet."
		if judging.OwnCandidates > 0 {
			score.Done = true
			score.Detail = "All your teams have declared conflicts; nothing to score."
		}
	}
	if !queueOpen {
		score.BlockedReason = "judging_closed"
		if !closed {
			score.BlockedReason = "judging_not_open"
		}
	}
	facts["j.scores"] = score
	params["team"] = judging.FirstUnfinished
	detail := "No time set"
	if ev.JudgingEndsAt.Valid {
		detail = ev.JudgingEndsAt.Time.In(eventLocation(ev)).Format("Mon 2 Jan, 15:04")
	}
	facts["j.closes"] = rules.ChecklistFact{Done: ev.JudgingLockedAt.Valid || judging.Published || ev.Stage == "archived", Detail: detail, BlockedReason: "informational"}
}

func intPointer(n int) *int { return &n }
func eventLocation(ev model.Event) *time.Location {
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil || ev.TimeZone == "" {
		return time.UTC
	}
	return loc
}

func (s *ChecklistService) organiserFacts(ctx context.Context, ev model.Event, rubric []model.RubricCriterion, j entries.ChecklistJudgingFacts, f map[string]rules.ChecklistFact) error {
	set := func(id string, done bool) { f[id] = rules.ChecklistFact{Done: done} }
	// block marks ids blocked for reason; a later reason replaces an earlier one.
	block := func(reason string, ids ...string) {
		for _, id := range ids {
			v := f[id]
			v.BlockedReason = reason
			f[id] = v
		}
	}
	target := func(id, to string) {
		v := f[id]
		v.TargetID = to
		f[id] = v
	}
	validRules := true
	var stored struct {
		Fields map[string]string `json:"fields"`
	}
	if json.Unmarshal(ev.EntryRules, &stored) != nil || len(stored.Fields) == 0 {
		validRules = false
	}
	for field, mode := range stored.Fields {
		if !slices.Contains(hackentries.RuleFields, field) || !hackentries.RuleModeOK[mode] || mode == "one_of" && !hackentries.OneOfFields[field] {
			validRules = false
		}
	}
	set("o.entry-rules", ev.EntryRulesChosen && validRules)
	set("o.basics", rules.ChecklistBasicsReady(ev))
	set("o.times", ev.StartsAt.Valid && ev.EndsAt.Valid && !rules.Midnight(ev.StartsAt.Time, eventLocation(ev)) && !rules.Midnight(ev.EndsAt.Time, eventLocation(ev)))
	set("o.about", strings.TrimSpace(ev.About) != "")
	content, err := eventrepo.GetEventContent(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	// Stored content uses snake_case timestamps.
	type scheduleRow struct {
		Title   string `json:"title"`
		StartAt string `json:"start_at"`
		EndAt   string `json:"end_at"`
		Fixed   bool   `json:"fixed"`
	}
	var rows []scheduleRow
	_ = json.Unmarshal(content.Schedule, &rows)
	set("o.schedule", slices.ContainsFunc(rows, func(r scheduleRow) bool {
		a, e1 := time.Parse(time.RFC3339, r.StartAt)
		b, e2 := time.Parse(time.RFC3339, r.EndAt)
		_, ve := rules.Line(r.Title, "schedule", 1, 120)
		return !r.Fixed && ve == nil && e1 == nil && e2 == nil && b.After(a)
	}))
	var faq []struct{ Question, Answer string }
	_ = json.Unmarshal(content.FAQ, &faq)
	set("o.faq", slices.ContainsFunc(faq, func(r struct{ Question, Answer string }) bool {
		return strings.TrimSpace(r.Question) != "" && strings.TrimSpace(r.Answer) != ""
	}))
	var sponsors []struct{ Name string }
	_ = json.Unmarshal(content.Sponsors, &sponsors)
	set("o.sponsors", slices.ContainsFunc(sponsors, func(r struct{ Name string }) bool { return strings.TrimSpace(r.Name) != "" }))
	tracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	set("o.tracks", slices.ContainsFunc(tracks, func(t model.EventTrack) bool {
		_, e := rules.Tracks([]model.EventTrack{t})
		return e == nil
	}))
	set("o.rubric", entries.ChecklistRubricValid(rubric))
	mode := ev.JudgeAssignmentMode
	set("o.mode", mode == "open" || (mode == "automatic" || mode == "manual" || mode == "panel") && ev.JudgesPerTeam >= 1 && ev.JudgesPerTeam <= 20)
	set("o.tie-break", ev.TieCriterionID.Valid && slices.ContainsFunc(rubric, func(c model.RubricCriterion) bool { return ev.TieCriterionID.String == c.ID }))
	set("o.judging-closes", ev.JudgingEndsAt.Valid)
	if ev.Stage == "results" || ev.Stage == "archived" {
		block("judging_over", "o.judging-closes")
	}
	set("o.reminders", slices.ContainsFunc(ev.ReminderOffsets, func(offset int64) bool {
		return offset >= ReminderOffsetMin && offset <= ReminderOffsetMax
	}))
	announcements, err := eventrepo.ListEventAnnouncements(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	f["o.announcement"] = rules.ChecklistFact{Done: len(announcements) > 0, Detail: fmt.Sprintf("%d posted", len(announcements)), Count: intPointer(len(announcements))}
	set("o.deadline", ev.SubmissionDeadline.Valid)
	f["o.judges"] = rules.ChecklistFact{Done: j.Judges+j.Organisers > 0, Detail: judgeSelectionDetail(j.Judges + j.Organisers)}
	f["o.assignments"] = rules.ChecklistFact{Done: j.AssignmentsReady, Detail: j.AssignmentDetail, TargetID: j.Coverage.FixTarget}
	if ev.Stage == "results" || ev.Stage == "archived" || ev.JudgingLockedAt.Valid {
		block("judging_setup_locked", "o.judges")
	}
	f["o.judges-short"] = rules.ChecklistFact{Count: intPointer(j.TeamsShort)}
	f["o.scores"] = rules.ChecklistFact{Done: j.Judgeable > 0 && j.JudgesRequired > 0 && j.JudgesDone == j.JudgesRequired, Detail: fmt.Sprintf("%d of %d %s done", j.JudgesDone, j.JudgesRequired, checklistPlural(j.JudgesRequired, "judge")), Count: intPointer(j.JudgesDone), Total: intPointer(j.JudgesRequired)}
	if j.JudgesRequired == 0 {
		v := f["o.scores"]
		v.Detail = "No scores yet"
		f["o.scores"] = v
	}
	set("o.lock", ev.JudgingLockedAt.Valid)
	set("o.ties", j.TiesDone)
	if !j.HasStandings {
		f["o.ties"] = rules.ChecklistFact{Detail: "No standings yet", BlockedReason: "informational"}
	}
	set("o.publish", j.Published)
	if !ev.JudgingLockedAt.Valid {
		target("o.publish", "o.lock")
	}
	set("o.end", ev.Stage == "archived")
	participants, _, _, err := eventrepo.CountEventMembers(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	if participants == 0 {
		block("informational", "o.end")
	}
	block("informational", "o.scores")
	judgingSetupClosed := ev.Stage == "judging" || ev.Stage == "results"
	if judgingSetupClosed {
		block("judging_setup_locked", "o.rubric", "o.mode", "o.tie-break", "o.assignments")
	}
	if len(rubric) == 0 && !judgingSetupClosed {
		target("o.tie-break", "o.rubric")
	}
	if !hackentries.RulesEditable(ev.Stage) {
		block("entry_rules_closed", "o.entry-rules")
	}
	if judgingSetupClosed {
		block("tie_rule_closed", "o.tie-break")
	}
	if ev.JudgingLockedAt.Valid {
		block("scores_locked", "o.rubric", "o.mode", "o.assignments", "o.scores")
	}
	return nil
}

func (s *ChecklistService) participantFacts(ctx context.Context, ev model.Event, m model.EventMember, f map[string]rules.ChecklistFact) (string, error) {
	next := "team"
	if m.TeamID.Valid {
		p, st, err := s.entries.ChecklistParticipant(ctx, ev, m)
		if err != nil {
			return "", err
		}
		f["p.team"] = rules.ChecklistFact{Done: true, Detail: p.TeamLine}
		f["p.summary"] = rules.ChecklistFact{Done: p.Filled >= p.Of, Count: intPointer(p.Filled), Total: intPointer(p.Of), Detail: hackentries.MissingMessage(p.SummaryMissing, hackentries.LoadRules(ev.EntryRules))}
		f["p.evidence"] = rules.ChecklistFact{Done: p.EvidenceDone, Detail: p.Summary, TargetID: evidenceTarget(p.EvidenceMissing, hackentries.LoadRules(ev.EntryRules))}
		f["p.submit"] = rules.ChecklistFact{Done: p.Entry.Status() == "submitted", Settled: p.Entry.Status() == "not_submitted", Detail: p.Entry.Status()}
		next = p.Next
		if sub := f["p.submit"]; !sub.Done && !sub.Settled && p.Next != "submit" {
			// Submit has nothing to do until the steps above are done; they carry the actions.
			sub.BlockedReason = "entry_not_ready"
			f["p.submit"] = sub
		}
		if st.WriteRefusal() != "" {
			for _, id := range []string{"p.summary", "p.evidence", "p.submit"} {
				v := f[id]
				v.BlockedReason = st.WriteRefusal()
				f[id] = v
			}
		}
	}
	if !m.TeamID.Valid {
		for _, id := range []string{"p.summary", "p.evidence", "p.submit"} {
			f[id] = rules.ChecklistFact{BlockedReason: "join_team_first"}
		}
		if ev.Stage != "open" && ev.Stage != "building" {
			f["p.team"] = rules.ChecklistFact{BlockedReason: "teams_locked"}
		}
	}
	approved, err := s.entries.ChecklistParticipantApproved(ctx, ev, m.UserID)
	if err != nil {
		return "", err
	}
	if !approved || ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived" || ev.ResultsPublishedAt.Valid {
		for _, id := range []string{"p.team", "p.summary", "p.evidence", "p.submit"} {
			v := f[id]
			v.BlockedReason = "entry_unavailable"
			f[id] = v
		}
		return "", nil
	}
	return next, nil
}

// Write validates membership and stage again under the event lock, sharing the
// same transaction as the mark or acknowledgement. No synthetic admin writes.
func (s *ChecklistService) Write(ctx context.Context, eventID, actorID, itemID, version string, notNeeded *bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return err
	}
	m, err := eventrepo.GetEventMember(ctx, tx, eventID, actorID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return err
	}
	if ev.Stage == "archived" {
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if ev.TakenDown() {
		return &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	if notNeeded != nil {
		if m.Role != "organiser" {
			return &Error{Code: "checklist_role_forbidden", Message: "only organisers can mark optional items"}
		}
		allowed := false
		for _, d := range rules.ChecklistCatalogue() {
			if d.ID == itemID && strings.HasPrefix(d.ID, "o.") && d.Optional {
				allowed = true
			}
		}
		if !allowed {
			return &Error{Code: "invalid_checklist_item", Message: "only optional organiser items can be marked not needed"}
		}
		err = eventrepo.SetChecklistNotNeeded(ctx, tx, eventID, itemID, actorID, *notNeeded)
	} else {
		subject := actorID
		switch itemID {
		case "j.rubric", "j.conflicts":
			if judge, err := entryrepo.EventJudgeInEvent(ctx, tx, eventID, actorID); err != nil {
				return err
			} else if !judge {
				return &Error{Code: "checklist_role_forbidden", Message: "only judges can acknowledge this item"}
			}
		default:
			return &Error{Code: "invalid_checklist_item", Message: "this item has no acknowledgement"}
		}
		if itemID != "j.conflicts" {
			rubric, e := entryrepo.ListRubric(ctx, tx, eventID)
			if e != nil {
				return e
			}
			current := rules.RubricFingerprint(rubric)
			if current == "" || version != current {
				return &Error{Code: "stale_checklist_version", Message: "the rubric changed; read the checklist again"}
			}
		} else {
			version = ""
		}
		err = eventrepo.AcknowledgeChecklist(ctx, tx, eventID, subject, itemID, version, actorID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func judgeReadyDetail(judges, organisers int) string {
	detail := fmt.Sprintf("%d %s joined", judges, checklistPlural(judges, "judge"))
	if judges == 0 && organisers > 0 {
		verb := "can judge"
		detail += fmt.Sprintf(" · %d %s %s", organisers, checklistPlural(organisers, "organiser"), verb)
	}
	return detail
}
func checklistPlural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func evidenceTarget(missing []string, fields map[string]string) string {
	for _, field := range missing {
		switch field {
		case "video_url":
			return "p.evidence-video"
		case "code_url":
			return "p.evidence-code"
		case "screenshot":
			return "p.evidence-screenshot"
		case "one_of":
			if fields["video_url"] == "one_of" {
				return "p.evidence-video"
			}
			if fields["code_url"] == "one_of" {
				return "p.evidence-code"
			}
			if fields["screenshot"] == "one_of" {
				return "p.evidence-screenshot"
			}
		}
	}
	return "p.website"
}

func judgeSelectionDetail(n int) string {
	if n > 0 {
		return fmt.Sprintf("%d %s ready", n, checklistPlural(n, "judge"))
	}
	return "Share the judge link, or tick organisers who judge."
}
