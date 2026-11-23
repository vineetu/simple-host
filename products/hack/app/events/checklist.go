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

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
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

func (s *ChecklistService) Read(ctx context.Context, eventID, actorID, role string, adminView bool, forward *rules.ChecklistNext, now time.Time) (Checklist, error) {
	ev, err := eventrepo.GetEventByID(ctx, s.db, eventID)
	if err != nil {
		return Checklist{}, err
	}
	member, readOnly, err := s.checklistMember(ctx, eventID, actorID, adminView)
	if err != nil {
		return Checklist{}, err
	}
	if role, err = readableRole(role, member.Role, readOnly); err != nil {
		return Checklist{}, err
	}
	if role == "judge" {
		if err := requireJudge(ctx, s.db, eventID, actorID, "only judges can read this checklist"); err != nil {
			return Checklist{}, err
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
	participantNext, err := s.roleFacts(ctx, ev, member, role, subject, acks, facts, params)
	if err != nil {
		return Checklist{}, err
	}
	if ev.TakenDown() {
		readOnly = true
		for _, d := range rules.ChecklistCatalogue() {
			blockFacts(facts, "event_taken_down", d.ID)
		}
	}
	out := rules.BuildChecklist(rules.ChecklistInput{Role: role, Stage: ev.Stage, MembershipRole: member.Role, AssignmentMode: ev.JudgeAssignmentMode, JudgesPerTeam: ev.JudgesPerTeam, ReadOnly: readOnly, Facts: facts, Marks: marks, Forward: forward, ParticipantNext: participantNext}, now)
	s.linkChecklist(&out, ev.Slug, params)
	return out, nil
}

// checklistMember loads the reader's membership; an admin inspecting an event they are not in reads it as a read-only organiser.
func (s *ChecklistService) checklistMember(ctx context.Context, eventID, actorID string, adminView bool) (model.EventMember, bool, error) {
	member, err := hackmembers.Member(ctx, s.db, eventID, actorID, hackmembers.NoLock)
	member, err = hackmembers.WithDisplayName(ctx, s.db, member, err)
	if errors.Is(err, sql.ErrNoRows) && adminView {
		return model.EventMember{Role: "organiser"}, true, nil
	}
	return member, false, err
}

func readableRole(role, memberRole string, readOnly bool) (string, error) {
	if role == "" {
		role = memberRole
	}
	organiserJudging := memberRole == "organiser" && role == "judge" && !readOnly
	if role != memberRole && !organiserJudging {
		return "", &Error{Code: "checklist_role_forbidden", Message: "this membership cannot read that checklist"}
	}
	switch role {
	case "organiser", "judge", "participant":
		return role, nil
	}
	return "", &Error{Code: "checklist_role_forbidden", Message: "unknown checklist role"}
}

// requireJudge refuses actors who are not judges of the event, with message as the reason.
func requireJudge(ctx context.Context, q sqlkit.Querier, eventID, actorID, message string) error {
	judge, err := entryrepo.EventJudgeInEvent(ctx, q, eventID, actorID)
	if err != nil {
		return err
	}
	if !judge {
		return &Error{Code: "checklist_role_forbidden", Message: message}
	}
	return nil
}

// roleFacts fills the facts and link params for role and returns the participant's next step (empty for other roles).
func (s *ChecklistService) roleFacts(ctx context.Context, ev model.Event, member model.EventMember, role, subject string, acks map[string]string, facts map[string]rules.ChecklistFact, params map[string]string) (string, error) {
	if role == "participant" {
		return s.participantFacts(ctx, ev, member, facts)
	}
	rubric, err := entryrepo.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return "", err
	}
	if role == "judge" {
		judging, err := s.entries.ChecklistJudging(ctx, ev, subject, false)
		if err != nil {
			return "", err
		}
		judgeFacts(ev, member, judging, rules.RubricFingerprint(rubric), acks, facts, params)
		return "", nil
	}
	judging, err := s.entries.ChecklistOrganiserJudging(ctx, s.db, ev, rubric)
	if err != nil {
		return "", err
	}
	if err := s.organiserFacts(ctx, ev, rubric, judging, facts); err != nil {
		return "", err
	}
	if team, ok := tracklessTeam(judging.Coverage); ok {
		params["team"] = team
	}
	return "", nil
}

// tracklessTeam names the first short team missing a track when that is what the assignments item points the organiser at.
func tracklessTeam(c entries.SetupCoverage) (string, bool) {
	if c.FixTarget != "o.team-track" {
		return "", false
	}
	for _, t := range c.Short {
		if t.MissingTrack {
			return t.Slug, true
		}
	}
	return "", false
}

func (s *ChecklistService) linkChecklist(out *Checklist, slug string, params map[string]string) {
	for i := range out.Items {
		if link, ok := s.targets.Resolve(out.Items[i].TargetID, slug, params); ok {
			out.Items[i].Link = &link
		}
	}
	if out.Next == nil || out.Next.TargetID == "" {
		return
	}
	if link, ok := s.targets.Resolve(out.Next.TargetID, slug, params); ok {
		out.Next.Link = &link
	}
}

// judgeFacts fills a judge's checklist facts and the queue's first team.
func judgeFacts(ev model.Event, member model.EventMember, judging entries.ChecklistJudgingFacts, version string, acks map[string]string, facts map[string]rules.ChecklistFact, params map[string]string) {
	closed := ev.JudgingLockedAt.Valid || judging.Published || ev.Stage == "results" || ev.Stage == "archived"
	queueOpen := ev.Stage == "judging" && !closed
	facts["j.invite"] = rules.ChecklistFact{Done: !member.JoinedAt.IsZero(), BlockedReason: "informational"}
	facts["j.coc"] = rules.ChecklistFact{Done: member.CocAcceptedAt.Valid, BlockedReason: "conduct_record_only"}
	facts["j.rubric"] = judgeRubricFact(version, acks["j.rubric"], judging)
	if rubric := facts["j.rubric"]; closed && !rubric.Done {
		// Once scoring is closed there is nothing left to set up for.
		rubric.BlockedReason = "judging_closed"
		facts["j.rubric"] = rubric
	}
	_, conflictAck := acks["j.conflicts"]
	conflicts := rules.ChecklistFact{Done: judging.OwnConflicts > 0 || conflictAck}
	switch {
	case closed:
		conflicts.BlockedReason = "judging_closed"
	case !queueOpen:
		conflicts.BlockedReason = "judging_not_open"
	}
	facts["j.conflicts"] = conflicts
	facts["j.scores"] = judgeScoreFact(judging, queueOpen, closed)
	params["team"] = judging.FirstUnfinished
	closesDone := ev.JudgingLockedAt.Valid || judging.Published || ev.Stage == "archived"
	detail := "No time set"
	switch {
	case closesDone:
		detail = "Closed"
	case ev.JudgingEndsAt.Valid:
		detail = ev.JudgingEndsAt.Time.In(eventLocation(ev)).Format("Mon 2 Jan, 15:04")
	}
	facts["j.closes"] = rules.ChecklistFact{Done: closesDone, Detail: detail, BlockedReason: "informational"}
}

func judgeScoredAll(j entries.ChecklistJudgingFacts) bool {
	return j.OwnTotal > 0 && j.OwnDone == j.OwnTotal
}

// judgeRubricFact is done once the judge acknowledged this rubric version or scored their whole queue; with no rubric and nothing scored it waits.
func judgeRubricFact(version, ack string, j entries.ChecklistJudgingFacts) rules.ChecklistFact {
	if version == "" && !judgeScoredAll(j) {
		return rules.ChecklistFact{Detail: "The organiser hasn't added a rubric yet", BlockedReason: "no_rubric"}
	}
	return rules.ChecklistFact{Done: (version != "" && ack == version) || judgeScoredAll(j), Version: version}
}

// judgeScoreFact counts the judge's scored teams, explains an empty queue, and blocks scoring while judging is not open or already closed.
func judgeScoreFact(j entries.ChecklistJudgingFacts, queueOpen, closed bool) rules.ChecklistFact {
	score := rules.ChecklistFact{Done: judgeScoredAll(j), Detail: fmt.Sprintf("%d of %d %s scored", j.OwnDone, j.OwnTotal, checklistPlural(j.OwnTotal, "team")), Count: intPointer(j.OwnDone), Total: intPointer(j.OwnTotal)}
	if j.FirstUnfinished == "" || j.OwnTotal == 0 {
		score.TargetID = "j.queue"
	}
	if j.OwnTotal == 0 {
		score.BlockedReason = "nothing_assigned"
		score.Detail = "No teams are assigned to you yet."
		if j.OwnCandidates > 0 {
			score.Done = true
			score.Detail = "You know someone on every team you were given, so there is nothing to score."
		}
	}
	switch {
	case closed:
		score.BlockedReason = "judging_closed"
	case !queueOpen:
		score.BlockedReason = "judging_not_open"
	}
	return score
}

func intPointer(n int) *int { return &n }
func eventLocation(ev model.Event) *time.Location {
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil || ev.TimeZone == "" {
		return time.UTC
	}
	return loc
}

// organiserFacts loads what the organiser checklist reads beyond the event and fills its facts.
func (s *ChecklistService) organiserFacts(ctx context.Context, ev model.Event, rubric []model.RubricCriterion, j entries.ChecklistJudgingFacts, f map[string]rules.ChecklistFact) error {
	content, err := eventrepo.GetEventContent(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	tracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	announcements, err := eventrepo.ListEventAnnouncements(ctx, s.db, ev.ID)
	if err != nil {
		return err
	}
	participants, err := hackmembers.Count(ctx, s.db, hackmembers.Filter{EventID: ev.ID, Role: "participant", Status: "approved"})
	if err != nil {
		return err
	}
	organiserSetupFacts(ev, rubric, content, tracks, f)
	f["o.announcement"] = rules.ChecklistFact{Done: len(announcements) > 0, Detail: fmt.Sprintf("%d posted", len(announcements)), Count: intPointer(len(announcements))}
	organiserJudgingFacts(ev, j, f)
	blockOrganiserFacts(ev, participants == 0, f)
	return nil
}

func organiserSetupFacts(ev model.Event, rubric []model.RubricCriterion, content eventrepo.EventContent, tracks []model.EventTrack, f map[string]rules.ChecklistFact) {
	for id, done := range map[string]bool{
		"o.entry-rules":    ev.EntryRulesChosen && entryRulesValid(ev.EntryRules),
		"o.basics":         rules.ChecklistBasicsReady(ev),
		"o.times":          timesReady(ev),
		"o.about":          strings.TrimSpace(ev.About) != "",
		"o.schedule":       scheduleReady(content.Schedule),
		"o.faq":            faqReady(content.FAQ),
		"o.sponsors":       sponsorsReady(content.Sponsors),
		"o.tracks":         slices.ContainsFunc(tracks, trackValid),
		"o.mode":           assignmentModeReady(ev.JudgeAssignmentMode, ev.JudgesPerTeam),
		"o.tie-break":      tieBreakReady(ev, rubric),
		"o.judging-closes": ev.JudgingEndsAt.Valid,
		"o.reminders":      slices.ContainsFunc(ev.ReminderOffsets, reminderOffsetValid),
		"o.deadline":       ev.SubmissionDeadline.Valid,
	} {
		f[id] = rules.ChecklistFact{Done: done}
	}
	f["o.rubric"] = rules.ChecklistFact{Done: entries.ChecklistRubricValid(rubric), Version: rules.RubricFingerprint(rubric)}
	if len(rubric) == 0 && !judgingSetupClosed(ev.Stage) {
		f["o.tie-break"] = rules.ChecklistFact{Done: f["o.tie-break"].Done, TargetID: "o.rubric"}
	}
}

func organiserJudgingFacts(ev model.Event, j entries.ChecklistJudgingFacts, f map[string]rules.ChecklistFact) {
	f["o.judges"] = rules.ChecklistFact{Done: j.Judges+j.Organisers > 0, Detail: judgeSelectionDetail(j.Judges + j.Organisers)}
	f["o.assignments"] = rules.ChecklistFact{Done: j.AssignmentsReady, Detail: j.AssignmentDetail, TargetID: j.Coverage.FixTarget}
	f["o.judges-short"] = rules.ChecklistFact{Count: intPointer(j.TeamsShort)}
	scores := rules.ChecklistFact{Done: j.Judgeable > 0 && j.JudgesRequired > 0 && j.JudgesDone == j.JudgesRequired, Detail: fmt.Sprintf("%d of %d %s done", j.JudgesDone, j.JudgesRequired, checklistPlural(j.JudgesRequired, "judge")), Count: intPointer(j.JudgesDone), Total: intPointer(j.JudgesRequired)}
	if j.JudgesRequired == 0 {
		scores.Detail = "No scores yet"
	}
	f["o.scores"] = scores
	f["o.lock"] = rules.ChecklistFact{Done: ev.JudgingLockedAt.Valid}
	f["o.ties"] = rules.ChecklistFact{Done: j.TiesDone}
	if !j.HasStandings {
		f["o.ties"] = rules.ChecklistFact{Detail: "No standings yet", BlockedReason: "informational"}
	}
	f["o.publish"] = rules.ChecklistFact{Done: j.Published}
	if !ev.JudgingLockedAt.Valid {
		f["o.publish"] = rules.ChecklistFact{Done: j.Published, TargetID: "o.lock"}
	}
	f["o.end"] = rules.ChecklistFact{Done: ev.Stage == "archived"}
}

// blockOrganiserFacts marks organiser items the stage no longer lets them act on.
func blockOrganiserFacts(ev model.Event, noParticipants bool, f map[string]rules.ChecklistFact) {
	over := ev.Stage == "results" || ev.Stage == "archived"
	locked := ev.JudgingLockedAt.Valid
	setupClosed := judgingSetupClosed(ev.Stage)
	for _, b := range []struct {
		when   bool
		reason string
		ids    []string
	}{
		{over, "judging_over", []string{"o.judging-closes"}},
		{over || locked, "judging_setup_locked", []string{"o.judges"}},
		{noParticipants, "informational", []string{"o.end"}},
		{true, "informational", []string{"o.scores"}},
		{setupClosed, "judging_setup_locked", []string{"o.rubric", "o.mode", "o.assignments"}},
		{setupClosed, "tie_rule_closed", []string{"o.tie-break"}},
		{!hackentries.RulesEditable(ev.Stage), "entry_rules_closed", []string{"o.entry-rules"}},
		{locked, "scores_locked", []string{"o.rubric", "o.mode", "o.assignments", "o.scores"}},
	} {
		if b.when {
			blockFacts(f, b.reason, b.ids...)
		}
	}
}

func blockFacts(f map[string]rules.ChecklistFact, reason string, ids ...string) {
	for _, id := range ids {
		v := f[id]
		v.BlockedReason = reason
		f[id] = v
	}
}

// judgingSetupClosed reports stages where rubric, mode and tie rule are fixed.
func judgingSetupClosed(stage string) bool { return stage == "judging" || stage == "results" }

// entryRulesValid reports whether stored entry rules name at least one known field, each with a mode it allows.
func entryRulesValid(raw []byte) bool {
	var stored struct {
		Fields map[string]string `json:"fields"`
	}
	if json.Unmarshal(raw, &stored) != nil || len(stored.Fields) == 0 {
		return false
	}
	for field, mode := range stored.Fields {
		if !slices.Contains(hackentries.RuleFields, field) || !hackentries.RuleModeOK[mode] || mode == "one_of" && !hackentries.OneOfFields[field] {
			return false
		}
	}
	return true
}

// timesReady reports whether both event times are set and neither is a bare midnight.
func timesReady(ev model.Event) bool {
	loc := eventLocation(ev)
	return ev.StartsAt.Valid && ev.EndsAt.Valid && !rules.Midnight(ev.StartsAt.Time, loc) && !rules.Midnight(ev.EndsAt.Time, loc)
}

// scheduleReady reports whether stored schedule content (snake_case timestamps) has an organiser-added row with a valid title and time range.
func scheduleReady(raw []byte) bool {
	var rows []struct {
		Title   string `json:"title"`
		StartAt string `json:"start_at"`
		EndAt   string `json:"end_at"`
		Fixed   bool   `json:"fixed"`
	}
	_ = json.Unmarshal(raw, &rows)
	for _, r := range rows {
		a, e1 := time.Parse(time.RFC3339, r.StartAt)
		b, e2 := time.Parse(time.RFC3339, r.EndAt)
		_, ve := rules.Line(r.Title, "schedule", 1, 120)
		if !r.Fixed && ve == nil && e1 == nil && e2 == nil && b.After(a) {
			return true
		}
	}
	return false
}

// faqReady reports whether stored FAQ content has a question with an answer.
func faqReady(raw []byte) bool {
	var faq []struct{ Question, Answer string }
	_ = json.Unmarshal(raw, &faq)
	for _, r := range faq {
		if strings.TrimSpace(r.Question) != "" && strings.TrimSpace(r.Answer) != "" {
			return true
		}
	}
	return false
}

// sponsorsReady reports whether stored sponsor content names a sponsor.
func sponsorsReady(raw []byte) bool {
	var sponsors []struct{ Name string }
	_ = json.Unmarshal(raw, &sponsors)
	return slices.ContainsFunc(sponsors, func(r struct{ Name string }) bool { return strings.TrimSpace(r.Name) != "" })
}

func trackValid(t model.EventTrack) bool {
	_, err := rules.Tracks([]model.EventTrack{t})
	return err == nil
}

// assignmentModeReady reports whether the judge assignment mode is usable as configured.
func assignmentModeReady(mode string, judgesPerTeam int) bool {
	if mode == "open" {
		return true
	}
	return (mode == "automatic" || mode == "manual" || mode == "panel") && judgesPerTeam >= 1 && judgesPerTeam <= 20
}

// tieBreakReady reports whether the tie-break criterion is still in the rubric.
func tieBreakReady(ev model.Event, rubric []model.RubricCriterion) bool {
	return ev.TieCriterionID.Valid && slices.ContainsFunc(rubric, func(c model.RubricCriterion) bool { return ev.TieCriterionID.String == c.ID })
}

// reminderOffsetValid reports whether a reminder offset is within the allowed range.
func reminderOffsetValid(offset int64) bool {
	return offset >= ReminderOffsetMin && offset <= ReminderOffsetMax
}

func (s *ChecklistService) participantFacts(ctx context.Context, ev model.Event, m model.EventMember, f map[string]rules.ChecklistFact) (string, error) {
	next := "team"
	if m.TeamID.Valid {
		p, st, err := s.entries.ChecklistParticipant(ctx, ev, m)
		if err != nil {
			return "", err
		}
		next = p.Next
		teamEntryFacts(f, p, hackentries.LoadRules(ev.EntryRules))
		if reason := st.WriteRefusal(); reason != "" {
			blockFacts(f, reason, "p.summary", "p.evidence", "p.submit")
		}
	} else {
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
		blockFacts(f, "entry_unavailable", "p.team", "p.summary", "p.evidence", "p.submit")
		return "", nil
	}
	return next, nil
}

// teamEntryFacts fills a team member's entry steps from the team's progress.
func teamEntryFacts(f map[string]rules.ChecklistFact, p entries.Progress, fields map[string]string) {
	f["p.team"] = rules.ChecklistFact{Done: true, Detail: p.TeamLine}
	f["p.summary"] = rules.ChecklistFact{Done: p.Filled >= p.Of, Count: intPointer(p.Filled), Total: intPointer(p.Of), Detail: hackentries.MissingMessage(p.SummaryMissing, fields)}
	f["p.evidence"] = rules.ChecklistFact{Done: p.EvidenceDone, Detail: p.Summary, TargetID: evidenceTarget(p.EvidenceMissing, fields)}
	status := p.Entry.Status()
	submit := rules.ChecklistFact{Done: status == "submitted", Settled: status == "not_submitted", Detail: status}
	if !submit.Done && !submit.Settled && p.Next != "submit" {
		submit.BlockedReason = "entry_not_ready"
	}
	f["p.submit"] = submit
}

// Write validates membership and stage again under the event lock, sharing the same transaction as the mark or acknowledgement.
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
	m, err := hackmembers.Member(ctx, tx, eventID, actorID, hackmembers.NoLock)
	m, err = hackmembers.WithDisplayName(ctx, tx, m, err)
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
		err = markNotNeeded(ctx, tx, eventID, actorID, m.Role, itemID, *notNeeded)
	} else {
		err = acknowledge(ctx, tx, eventID, actorID, itemID, version)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// markNotNeeded lets an organiser mark an optional organiser item not needed.
func markNotNeeded(ctx context.Context, tx *sql.Tx, eventID, actorID, role, itemID string, notNeeded bool) error {
	if role != "organiser" {
		return &Error{Code: "checklist_role_forbidden", Message: "only organisers can mark optional items"}
	}
	optional := slices.ContainsFunc(rules.ChecklistCatalogue(), func(d rules.ChecklistDefinition) bool {
		return d.ID == itemID && strings.HasPrefix(d.ID, "o.") && d.Optional
	})
	if !optional {
		return &Error{Code: "invalid_checklist_item", Message: "only optional organiser items can be marked not needed"}
	}
	return eventrepo.SetChecklistNotNeeded(ctx, tx, eventID, itemID, actorID, notNeeded)
}

// acknowledge records a judge's acknowledgement of the rubric (at the version they read, which must still be current) or of their conflicts.
func acknowledge(ctx context.Context, tx *sql.Tx, eventID, actorID, itemID, version string) error {
	if itemID != "j.rubric" && itemID != "j.conflicts" {
		return &Error{Code: "invalid_checklist_item", Message: "this item has no acknowledgement"}
	}
	if err := requireJudge(ctx, tx, eventID, actorID, "only judges can acknowledge this item"); err != nil {
		return err
	}
	if itemID == "j.conflicts" {
		version = ""
	} else {
		rubric, err := entryrepo.ListRubric(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if current := rules.RubricFingerprint(rubric); current == "" || version != current {
			return &Error{Code: "stale_checklist_version", Message: "the rubric changed; read the checklist again"}
		}
	}
	return eventrepo.AcknowledgeChecklist(ctx, tx, eventID, actorID, itemID, version, actorID)
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
