package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// Need is one line of work or news awaiting a member. ID marks dismissable news; Count is for a live to-do.
type Need struct {
	Kind, ID, Text, Section string
	Count                   int
	At                      time.Time
	Team                    string
}

func clip(s string, n int) string {
	s = oneLine(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// NeedsYou lists what waits on this member in this event, live to-dos first, then unseen news, newest first.
func (s *Service) NeedsYou(ctx context.Context, ev model.Event, m model.EventMember, judges EntryEvaluator) []Need {
	items := []Need{}
	if m.JoinedAt.IsZero() || ev.TakenDown() {
		return items
	}
	switch m.Role {
	case "organiser":
		return append(items, s.organiserNeeds(ctx, ev, m.UserID)...)
	case "judge":
		return append(items, s.judgeNeeds(ctx, ev, m.UserID, judges)...)
	case "participant":
		return append(items, s.participantNeeds(ctx, ev, m.UserID)...)
	}
	return items
}

// organiserNeeds lists waiting applications and teams that wrote to the organisers and this organiser has not opened yet.
func (s *Service) organiserNeeds(ctx context.Context, ev model.Event, userID string) []Need {
	if ev.Stage == "archived" {
		return nil
	}
	var items []Need
	if n, err := hackmembers.CountWaitingApplications(ctx, s.db, ev.ID); err == nil && n > 0 {
		items = append(items, Need{Kind: hackmembers.NoticeApplicationNew, Count: n, Text: applicationsText(n), Section: "people"})
	}
	groups, err := hackmembers.InboundGroups(ctx, s.db, ev.ID, userID)
	if err != nil {
		return items
	}
	inbound, err := teamrepo.ListInboundTeams(ctx, s.db, groups)
	if err != nil {
		return items
	}
	for _, t := range inbound {
		text := t.TeamName + " wrote to the organisers: " + clip(t.Latest, 140)
		if t.Count > 1 {
			text = fmt.Sprintf("%s sent %d messages to the organisers. Latest: %s", t.TeamName, t.Count, clip(t.Latest, 140))
		}
		items = append(items, Need{Kind: hackmembers.NoticeTeamInbound, Count: t.Count, Text: text, Section: "team", Team: t.TeamSlug})
	}
	return items
}

func applicationsText(n int) string {
	if n == 1 {
		return "1 application is waiting for a decision."
	}
	return fmt.Sprintf("%d applications are waiting for a decision.", n)
}

func (s *Service) judgeNeeds(ctx context.Context, ev model.Event, userID string, judges EntryEvaluator) []Need {
	news, err := hackmembers.ListNews(ctx, s.db, ev.ID, userID)
	if err != nil || judges == nil {
		return nil
	}
	var items []Need
	for _, n := range news {
		if text, ok, err := s.judgingNoticeLine(ctx, s.db, n, ev.TimeZone, judges); err == nil && ok {
			items = append(items, Need{Kind: n.Kind, ID: n.ID, Count: 1, Text: text, Section: "queue", At: n.CreatedAt})
		}
	}
	return items
}

func (s *Service) participantNeeds(ctx context.Context, ev model.Event, userID string) []Need {
	m, err := hackmembers.Member(ctx, s.db, ev.ID, userID, hackmembers.NoLock)
	if err != nil || !m.Approved() {
		return nil
	}
	teamID, admin := m.TeamID, m.TeamAdmin
	var items []Need
	if finding := ev.TeamFinding && (ev.Stage == "open" || ev.Stage == "building"); finding {
		items = s.teamFindingNeeds(ctx, ev.ID, userID, teamID, admin)
	}
	news, err := hackmembers.ListNews(ctx, s.db, ev.ID, userID)
	if err != nil {
		return items
	}
	for _, n := range news {
		if text, section, ok := s.newsText(ctx, s.db, ev.Title, ev.TimeZone, n); ok {
			items = append(items, Need{Kind: n.Kind, ID: n.ID, Count: 1, Text: text, Section: section, At: n.CreatedAt})
		}
	}
	return items
}

// teamFindingNeeds is the join requests a team admin must answer, or the invites a teamless participant has.
func (s *Service) teamFindingNeeds(ctx context.Context, eventID, userID string, teamID sql.NullString, admin bool) []Need {
	if !teamID.Valid {
		invites, err := teamrepo.ListPendingInvites(ctx, s.db, eventID, userID)
		if err != nil || len(invites) == 0 {
			return nil
		}
		text := fmt.Sprintf("%d teams invited you to join.", len(invites))
		if len(invites) == 1 {
			text = invites[0].TeamName + " invited you to join."
		}
		return []Need{{Kind: hackmembers.NoticeInvite, Count: len(invites), Text: text, Section: "team"}}
	}
	if !admin {
		return nil
	}
	reqs, err := teamrepo.ListPendingJoinRequests(ctx, s.db, teamID.String)
	if err != nil || len(reqs) == 0 {
		return nil
	}
	team, _ := teamrepo.GetEventTeamByID(ctx, s.db, teamID.String)
	return []Need{{Kind: hackmembers.NoticeJoinRequest, Count: len(reqs), Text: joinRequestText(reqs, team.Name), Section: "team"}}
}

func joinRequestText(reqs []model.PendingJoinRequest, team string) string {
	if team == "" {
		team = "your team"
	}
	if len(reqs) == 1 {
		return oneLine(reqs[0].DisplayName) + " asked to join " + team + "."
	}
	return fmt.Sprintf("%d people asked to join %s.", len(reqs), team)
}

// oneLine keeps names and reasons on one line in an email.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func hackNoticeTime(t time.Time, zone string) string {
	loc, fallback := hackEventLocation(zone)
	at := t.In(loc)
	return at.Format("Mon 2 Jan 2006, 3:04 pm") + " " + hackZoneAbbr(at, fallback)
}

// newsText is the line for one news notice, from the team's state now (a renamed team reads with its new name), and its screen.
func (s *Service) newsText(ctx context.Context, q sqlkit.Querier, eventTitle, zone string, n model.Notice) (string, string, bool) {
	var team model.EventTeam
	if n.TeamID != "" {
		t, err := teamrepo.GetEventTeamByID(ctx, q, n.TeamID)
		if err != nil && hackmembers.NoticeTeamScoped(n.Kind) {
			return "", "", false
		}
		team = t
	}
	switch n.Kind {
	case hackmembers.NoticeDeadlineChanged:
		st, err := teamrepo.TeamWriteStateFor(ctx, q, team.EventID, team.ID, false)
		if err != nil {
			return "", "", false
		}
		return deadlineNewsText(st, zone), "entry", true
	case hackmembers.TeamAdminNotice:
		m, err := hackmembers.Member(ctx, q, n.EventID, n.UserID, hackmembers.NoLock)
		if err != nil || m.TeamID.String != n.TeamID || !m.TeamAdmin {
			return "", "", false
		}
		return "You're now an admin of " + team.Name + ". You answer requests to join and can lock the team.", "team", true
	case hackmembers.NoticeTeamMessage:
		body, err := teamrepo.NoticeTeamMessageBody(ctx, q, n.RefID, n.TeamID)
		if err != nil {
			return "", "", false
		}
		return "Message from the organisers: " + oneLine(body), "team", true
	}
	if text, section, ok := teamNewsText(eventTitle, team, n); ok {
		return text, section, true
	}
	return "", "", false
}

// teamNewsText is the line and screen for news that reads only the notice and the team as it is now; ok=false when the news no longer holds.
func teamNewsText(eventTitle string, team model.EventTeam, n model.Notice) (string, string, bool) {
	teamName := team.Name
	if teamName == "" {
		teamName = "The team"
	}
	switch n.Kind {
	case hackmembers.NoticeRequestAnswered:
		if n.Detail == "accepted" {
			return teamName + " accepted your request. You're on the team.", "team", true
		}
		return teamName + " said no to your request to join.", "team", true
	case hackmembers.NoticeApplicationDecided:
		// A rejection shows as the rejected card instead, never as a line.
		return "Your application was approved. Welcome to " + eventTitle + ".", "home", n.Detail == "approved"
	case hackmembers.NoticeSiteTakenDown:
		text := "The organisers took your team's site down."
		if r := oneLine(team.SiteTakenDownReason); r != "" {
			text += " Their reason: " + r
		}
		return text, "website", team.SiteTakenDownAt.Valid
	case hackmembers.NoticeSiteRestored:
		return "The organisers put your team's site back up.", "website", !team.SiteTakenDownAt.Valid
	case hackmembers.NoticeTeamLocked:
		return team.Name + " is locked, so nobody new can join.", "team", team.Locked()
	case hackmembers.NoticeTeamRemoved:
		return oneLine(n.Detail), "team", n.Detail != ""
	}
	return "", "", false
}

func deadlineNewsText(st model.TeamWriteState, zone string) string {
	if st.HasOverride && st.Deadline.Valid {
		return "Your team has until " + hackNoticeTime(st.Deadline.Time, zone) + " to finish its entry."
	}
	text := "Your team's extra time was taken away."
	if st.Deadline.Valid {
		text += " Entries close " + hackNoticeTime(st.Deadline.Time, zone) + "."
	}
	return text
}

// judgingNoticeLine re-reads the event and writes judging_open/ judging_reminder's line from state now.
func (s *Service) judgingNoticeLine(ctx context.Context, q sqlkit.Querier, n model.Notice, zone string, judges EntryEvaluator) (string, bool, error) {
	ev, err := hackevents.GetEventByID(ctx, q, n.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if ev.Stage != "judging" || ev.JudgingLockedAt.Valid {
		return "", false, nil
	}
	// Joined judges only: an organiser who judges gets no judging notices.
	m, err := hackmembers.Member(ctx, q, n.EventID, n.UserID, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !m.Judge(nil) {
		return "", false, nil
	}
	total, count, err := judges.QueueCounts(ctx, ev, n.UserID)
	if err != nil {
		return "", false, err
	}
	if n.Kind == hackmembers.NoticeJudgingOpen {
		return judgingOpenLine(total, count), true, nil
	}
	line, ok := judgingReminderLine(ev, n.Detail, count, zone)
	return line, ok, nil
}

func judgingOpenLine(total, count int) string {
	switch {
	case total == 0:
		return "Judging is open. No teams are in your queue yet."
	case count == 0:
		return "Judging is open. You've scored every team in your queue."
	}
	return "Judging is open: you have " + teamCount(count) + " to score."
}

func judgingReminderLine(ev model.Event, detail string, count int, zone string) (string, bool) {
	if count == 0 || !ev.JudgingEndsAt.Valid || !ev.JudgingEndsAt.Time.After(time.Now()) {
		return "", false
	}
	if reminderOffsetStale(ev.ReminderOffsets, detail, ev.JudgingEndsAt.Time, time.Until(ev.JudgingEndsAt.Time)) {
		return "", false
	}
	have := "You still have " + teamCount(count) + " to score."
	return "Judging closes " + hackNoticeTime(ev.JudgingEndsAt.Time, zone) + " (" + ReminderIn(time.Until(ev.JudgingEndsAt.Time)) + "). " + have, true
}

func teamCount(n int) string {
	if n == 1 {
		return "1 team"
	}
	return fmt.Sprintf("%d teams", n)
}

// sendNoticeBatch handles one person and kind. more=false when nothing was due.
func (s *Service) sendNoticeBatch(ctx context.Context, sender interface {
	SendNotice(string, string, string) error
}, delay, window time.Duration, publicBaseURL string, evaluator EntryEvaluator) (bool, int64, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	txCtx, cancelTx := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTx()
	stopCancel := context.AfterFunc(ctx, cancelTx)
	defer stopCancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	b, err := hackmembers.NextMailBatch(ctx, tx, delay, window)
	if errors.Is(err, sql.ErrNoRows) || hackmembers.IsMailBatchRaced(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	groups, valid, stale, err := s.groupMailLines(ctx, tx, b, evaluator)
	if err == nil {
		err = hackmembers.FinishMailNotices(ctx, tx, stale, "skipped")
	}
	if err != nil {
		return false, 0, err
	}
	if len(valid) > 0 {
		// Once sending starts, worker cancellation must not roll back a successful provider response before its durable mark is committed.
		if !stopCancel() || ctx.Err() != nil {
			return false, 0, ctx.Err()
		}
		sendErr := sendMailNotice(ctx, sender, b.Recipient, noticeMailSubject(groups, b.Kind), noticeMailBody(groups, b.Kind, publicBaseURL))
		finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelFinish()
		defer context.AfterFunc(finishCtx, cancelTx)()
		if err := markMailSent(finishCtx, tx, valid, sendErr); err != nil {
			return false, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, int64(len(stale) + len(valid)), nil
}

type mailGroup struct {
	slug, title string
	lines       []string
	apps        int
}

// groupMailLines re-checks each notice of the batch and groups the lines that still hold by event, in first-seen order.
func (s *Service) groupMailLines(ctx context.Context, tx *sql.Tx, b model.MailBatch, evaluator EntryEvaluator) (groups []*mailGroup, valid, stale []string, err error) {
	byEvent := map[string]*mailGroup{}
	for _, n := range b.Notices {
		line, ok, err := s.mailLine(ctx, tx, n, evaluator)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok || b.Recipient == "" {
			stale = append(stale, n.ID)
			continue
		}
		valid = append(valid, n.ID)
		g := byEvent[n.EventID]
		if g == nil {
			g = &mailGroup{slug: n.EventSlug, title: n.EventTitle}
			byEvent[n.EventID] = g
			groups = append(groups, g)
		}
		switch {
		case n.Kind == hackmembers.NoticeApplicationNew:
			g.apps++
		case !slices.Contains(g.lines, line):
			g.lines = append(g.lines, line)
		}
	}
	return groups, valid, stale, nil
}

// markMailSent records the send: delivered notices are finished, a failed delivery defers them for a later sweep.
func markMailSent(ctx context.Context, tx *sql.Tx, valid []string, sendErr error) error {
	if sendErr != nil {
		log.Printf("hack: notice mail delivery failed: %v", sendErr)
		return hackmembers.DeferMailNotices(ctx, tx, valid)
	}
	return hackmembers.FinishMailNotices(ctx, tx, valid, "sent")
}

func noticeMailSubject(groups []*mailGroup, kind string) string {
	if len(groups) == 1 {
		return oneLine(groups[0].title) + ": " + noticeSubject(kind)
	}
	return "Simple Hack: " + noticeSubject(kind)
}

func noticeMailBody(groups []*mailGroup, kind, publicBaseURL string) string {
	var body strings.Builder
	for i, g := range groups {
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(oneLine(g.title) + "\n")
		switch {
		case g.apps == 1:
			body.WriteString("1 new application is waiting for a decision.\n")
		case g.apps > 1:
			fmt.Fprintf(&body, "%d new applications are waiting for a decision.\n", g.apps)
		}
		for _, line := range g.lines[:min(len(g.lines), 20)] {
			body.WriteString(line + "\n")
		}
		if len(g.lines) > 20 {
			fmt.Fprintf(&body, "And %d more.\n", len(g.lines)-20)
		}
		body.WriteString(noticeLink(kind, g.slug, publicBaseURL) + "\n")
	}
	body.WriteString("\nYou get these because your Simple Hack account uses this address. Turn them off on your Account page: " + publicBaseURL + "/account\n")
	return body.String()
}

var noticeSubjects = map[string]string{
	hackmembers.NoticeJoinRequest:        "someone asked to join your team",
	hackmembers.NoticeInvite:             "a team invited you to join",
	hackmembers.NoticeRequestAnswered:    "your request to join a team has an answer",
	hackmembers.NoticeApplicationNew:     "applications are waiting for you",
	hackmembers.NoticeApplicationDecided: "your application has an answer",
	hackmembers.NoticeSiteTakenDown:      "your team's site was taken down",
	hackmembers.NoticeSiteRestored:       "your team's site is back up",
	hackmembers.NoticeDeadlineChanged:    "your team's deadline changed",
	hackmembers.TeamAdminNotice:          "you're now a team admin",
	hackmembers.NoticeTeamRemoved:        "you were taken off a team",
	hackmembers.NoticeEntryReminder:      "your entry is due soon",
	hackmembers.NoticeJudgingOpen:        "judging is open",
	hackmembers.NoticeJudgingReminder:    "judging closes soon",
	hackmembers.NoticeTeamMessage:        "a message from the organisers",
	hackmembers.NoticeTeamInbound:        "a team wrote to the organisers",
}

func noticeSubject(kind string) string {
	if subject, ok := noticeSubjects[kind]; ok {
		return subject
	}
	return "something is waiting for you"
}

func noticeLink(kind, slug, publicBaseURL string) string {
	base := publicBaseURL + "/e/" + slug
	switch kind {
	case hackmembers.NoticeApplicationNew:
		return base + "/manage?s=people"
	case hackmembers.NoticeApplicationDecided:
		return base + "/team"
	case hackmembers.NoticeSiteTakenDown, hackmembers.NoticeSiteRestored:
		return base + "/team?s=website"
	case hackmembers.NoticeDeadlineChanged, hackmembers.NoticeEntryReminder:
		return base + "/team?s=entry"
	case hackmembers.NoticeJudgingOpen, hackmembers.NoticeJudgingReminder:
		return base + "/judge?s=queue"
	case hackmembers.NoticeTeamInbound:
		return base + "/manage?s=teams"
	}
	return base + "/team?s=team"
}

// mailMember is who the notice's person is now, and the stage of its event.
type mailMember struct {
	model.EventMember
	stage string
}

// mailMemberOf reads mailMember; sql.ErrNoRows when they left or the event
// is taken down (NextMailBatch read the event).
func mailMemberOf(ctx context.Context, q sqlkit.Querier, n model.MailNotice) (mailMember, error) {
	if n.EventTakenDown {
		return mailMember{}, sql.ErrNoRows
	}
	m, err := hackmembers.Member(ctx, q, n.EventID, n.UserID, hackmembers.NoLock)
	return mailMember{m, n.EventStage}, err
}

func (m mailMember) receives(n model.MailNotice) bool {
	onTeam := m.TeamID.Valid && m.TeamID.String == n.TeamID
	if hackmembers.NoticeTeamScoped(n.Kind) && !onTeam {
		return false
	}
	finding := m.stage == "open" || m.stage == "building"
	participant := m.Participant()
	switch n.Kind {
	case hackmembers.NoticeJoinRequest:
		return finding && m.TeamAdmin && onTeam
	case hackmembers.NoticeInvite:
		return finding && participant && !m.TeamID.Valid
	case hackmembers.NoticeApplicationNew, hackmembers.NoticeTeamInbound:
		return m.Organiser() && m.stage != "archived"
	case hackmembers.NoticeApplicationDecided:
		return participant && m.ApprovalStatus == n.Detail
	case hackmembers.NoticeRequestAnswered, hackmembers.NoticeTeamRemoved, hackmembers.NoticeEntryReminder:
		return participant
	}
	return true
}

func (s *Service) mailLine(ctx context.Context, tx *sql.Tx, n model.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	var m mailMember
	var err error
	m, err = mailMemberOf(ctx, tx, n)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !m.receives(n) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch n.Kind {
	case hackmembers.NoticeJoinRequest:
		name, team, err := teamrepo.MailRequest(ctx, tx, n.RefID, n.TeamID)
		return lineIfFound(oneLine(name)+" asked to join "+oneLine(team)+".", err)
	case hackmembers.NoticeInvite:
		team, err := teamrepo.MailInvite(ctx, tx, n.RefID, n.UserID)
		return lineIfFound(oneLine(team)+" invited you to join.", err)
	case hackmembers.NoticeApplicationNew:
		applicant, err := hackmembers.Member(ctx, tx, n.EventID, n.RefID, hackmembers.NoLock)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		pending := err == nil && applicant.Participant() && applicant.ApprovalStatus == "pending"
		return "", pending, err
	case hackmembers.NoticeApplicationDecided:
		if n.Detail == "approved" {
			return "Your application was approved. You're in.", true, nil
		}
		return "Your application wasn't accepted this time.", true, nil
	case hackmembers.NoticeTeamInbound:
		name, body, err := teamrepo.NoticeTeamInboundLine(ctx, tx, n.RefID, n.TeamID)
		return lineIfFound(oneLine(name)+" wrote to the organisers: "+clip(body, 200), err)
	case hackmembers.NoticeEntryReminder:
		return s.entryReminderLine(ctx, tx, n, evaluator)
	case hackmembers.NoticeJudgingOpen, hackmembers.NoticeJudgingReminder:
		text, ok, err := s.judgingNoticeLine(ctx, tx, n.Notice, n.EventTimeZone, evaluator)
		return oneLine(text), ok, err
	}
	text, _, ok := s.newsText(ctx, tx, n.EventTitle, n.EventTimeZone, n.Notice)
	return oneLine(text), ok, nil
}

// lineIfFound keeps text when its lookup found a row, drops the line on sql.ErrNoRows, and fails on any other error.
func lineIfFound(text string, err error) (string, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

func hackEventLocation(zone string) (*time.Location, string) {
	loc, err := time.LoadLocation(zone)
	if err != nil || loc == nil {
		return time.UTC, "UTC"
	}
	name := zone
	if name == "" {
		name = loc.String()
	}
	return loc, name
}

var hackNumericZoneRe = regexp.MustCompile(`^[+-]\d`)

// hackZoneAbbrFamiliar is the same fixed map ui.zoneAbbr.
var hackZoneAbbrFamiliar = map[string]string{
	"Asia/Jakarta": "WIB", "Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
	"Asia/Tokyo":     "JST",
	"Asia/Seoul":     "KST",
	"Asia/Singapore": "SGT",
	"Asia/Kolkata":   "IST", "Asia/Calcutta": "IST",
}

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT").
func hackZoneAbbr(t time.Time, ianaFallback string) string {
	abbr, offsetSeconds := t.Zone()
	if abbr != "" && !hackNumericZoneRe.MatchString(abbr) {
		return abbr
	}
	if a, ok := hackZoneAbbrFamiliar[ianaFallback]; ok {
		return a
	}
	return hackGMTOffsetLabel(offsetSeconds)
}

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or "GMT+5:30".
func hackGMTOffsetLabel(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return "GMT"
	}
	sign := "+"
	mins := offsetSeconds / 60
	if mins < 0 {
		sign = "-"
		mins = -mins
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("GMT%s%d", sign, h)
	}
	return fmt.Sprintf("GMT%s%d:%02d", sign, h, m)
}

// NoticeMailTiming gives notices time to be undone and limits repeat mail.
func NoticeMailTiming() (time.Duration, time.Duration) { return time.Minute, 15 * time.Minute }

func (s *Service) DeliverNoticeMail(ctx context.Context, sender NoticeSender, delay, window time.Duration, publicBaseURL string, evaluator EntryEvaluator) (written int64, resultErr error) {
	for i := 0; i < 50; i++ {
		more, n, err := s.sendNoticeBatch(ctx, sender, delay, window, publicBaseURL, evaluator)
		written += n
		if err != nil {
			log.Printf("hack: notice mail: %v", err)
			return written, err
		}
		if !more {
			return written, nil
		}
	}
	return written, nil
}

func (s *Service) PruneNotices(ctx context.Context) (int64, error) {
	n, err := hackmembers.PruneNoticesCount(ctx, s.db)
	if err != nil {
		log.Printf("hack: prune notices: %v", err)
	}
	return n, err
}

func (s *Service) MarkNewsSeen(ctx context.Context, eventID, userID string, ids []string) (int64, error) {
	return hackmembers.MarkNewsSeen(ctx, s.db, eventID, userID, ids)
}

type TodoEvent struct {
	Slug, Title, Role string
	Items             []Need
}

func (s *Service) Todos(ctx context.Context, userID string, judges EntryEvaluator) ([]TodoEvent, bool, bool, error) {
	events, members, err := memberships(ctx, s.db, userID)
	if err != nil {
		return nil, false, false, err
	}
	out := []TodoEvent{}
	for i, ev := range events {
		m := members[i]
		items := s.NeedsYou(ctx, ev, m, judges)
		if len(items) == 0 {
			continue
		}
		out = append(out, TodoEvent{ev.Slug, ev.Title, m.Role, items})
	}
	on, possible, err := hackmembers.EmailUpdates(ctx, s.db, userID)
	return out, on, possible, err
}
