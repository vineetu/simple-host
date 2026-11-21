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
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// Need is one line of work or news awaiting a member. ID marks dismissable news; Count is for a live to-do.
type Need struct {
	Kind, ID, Text, Section string
	Count                   int
	At                      time.Time
	// Team is the team slug when the item opens one team's page.
	Team string
}

// clip shortens a message to one line of at most n characters for a notice line.
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

// organiserNeeds lists waiting applications and, SIM-443, teams that wrote to the organisers and this organiser has not opened yet.
func (s *Service) organiserNeeds(ctx context.Context, ev model.Event, userID string) []Need {
	if ev.Stage == "archived" {
		return nil
	}
	var items []Need
	if n, err := hackevents.CountWaitingApplications(ctx, s.db, ev.ID); err == nil && n > 0 {
		items = append(items, Need{Kind: hackevents.NoticeApplicationNew, Count: n, Text: applicationsText(n), Section: "people"})
	}
	inbound, err := hackevents.ListInboundTeams(ctx, s.db, ev.ID, userID)
	if err != nil {
		return items
	}
	for _, t := range inbound {
		text := t.TeamName + " wrote to the organisers: " + clip(t.Latest, 140)
		if t.Count > 1 {
			text = fmt.Sprintf("%s sent %d messages to the organisers. Latest: %s", t.TeamName, t.Count, clip(t.Latest, 140))
		}
		items = append(items, Need{Kind: hackevents.NoticeTeamInbound, Count: t.Count, Text: text, Section: "team", Team: t.TeamSlug})
	}
	return items
}

// applicationsText is the to-do line for n waiting applications.
func applicationsText(n int) string {
	if n == 1 {
		return "1 application is waiting for a decision."
	}
	return fmt.Sprintf("%d applications are waiting for a decision.", n)
}

// judgeNeeds lists a judge's judging news that still holds.
func (s *Service) judgeNeeds(ctx context.Context, ev model.Event, userID string, judges EntryEvaluator) []Need {
	news, err := hackevents.ListNews(ctx, s.db, ev.ID, userID)
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

// participantNeeds lists an approved participant's team to-dos while teams form.
func (s *Service) participantNeeds(ctx context.Context, ev model.Event, userID string) []Need {
	status, teamID, admin, err := hackevents.NoticeMemberStatus(ctx, s.db, ev.ID, userID)
	if err != nil || status != "approved" {
		return nil
	}
	var items []Need
	if finding := ev.TeamFinding && (ev.Stage == "open" || ev.Stage == "building"); finding {
		items = s.teamFindingNeeds(ctx, ev.ID, userID, teamID, admin)
	}
	news, err := hackevents.ListNews(ctx, s.db, ev.ID, userID)
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
		invites, err := hackevents.ListPendingInvites(ctx, s.db, eventID, userID)
		if err != nil || len(invites) == 0 {
			return nil
		}
		text := fmt.Sprintf("%d teams invited you to join.", len(invites))
		if len(invites) == 1 {
			text = invites[0].TeamName + " invited you to join."
		}
		return []Need{{Kind: hackevents.NoticeInvite, Count: len(invites), Text: text, Section: "team"}}
	}
	if !admin {
		return nil
	}
	reqs, err := hackevents.ListPendingJoinRequests(ctx, s.db, teamID.String)
	if err != nil || len(reqs) == 0 {
		return nil
	}
	team, _ := teamrepo.GetEventTeamByID(ctx, s.db, teamID.String)
	return []Need{{Kind: hackevents.NoticeJoinRequest, Count: len(reqs), Text: joinRequestText(reqs, team.Name), Section: "team"}}
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
		if err != nil && hackevents.NoticeTeamScoped(n.Kind) {
			return "", "", false
		}
		team = t
	}
	switch n.Kind {
	case hackevents.NoticeDeadlineChanged:
		st, err := teamrepo.TeamWriteStateFor(ctx, q, team.ID, false)
		if err != nil {
			return "", "", false
		}
		return deadlineNewsText(st, zone), "entry", true
	case hackevents.TeamAdminNotice:
		admin, err := hackevents.NoticeTeamAdmin(ctx, q, n.EventID, n.UserID, n.TeamID)
		if err != nil || !admin {
			return "", "", false
		}
		return "You're now an admin of " + team.Name + ". You answer requests to join and can lock the team.", "team", true
	case hackevents.NoticeTeamMessage:
		body, err := hackevents.NoticeTeamMessageBody(ctx, q, n.RefID, n.TeamID)
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
	case hackevents.NoticeRequestAnswered:
		if n.Detail == "accepted" {
			return teamName + " accepted your request. You're on the team.", "team", true
		}
		return teamName + " said no to your request to join.", "team", true
	case hackevents.NoticeApplicationDecided:
		// A rejection shows as the rejected card instead, never as a line.
		return "Your application was approved. Welcome to " + eventTitle + ".", "home", n.Detail == "approved"
	case hackevents.NoticeSiteTakenDown:
		text := "The organisers took your team's site down."
		if r := oneLine(team.SiteTakenDownReason); r != "" {
			text += " Their reason: " + r
		}
		return text, "website", team.SiteTakenDownAt.Valid
	case hackevents.NoticeSiteRestored:
		return "The organisers put your team's site back up.", "website", !team.SiteTakenDownAt.Valid
	case hackevents.NoticeTeamLocked:
		return team.Name + " is locked, so nobody new can join.", "team", team.Locked()
	case hackevents.NoticeTeamRemoved:
		return oneLine(n.Detail), "team", n.Detail != ""
	}
	return "", "", false
}

// deadlineNewsText describes a team's deadline after an organiser changed its extra time.
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
	isJudge, err := hackevents.NoticeIsJudge(ctx, q, n.EventID, n.UserID)
	if err != nil {
		return "", false, err
	}
	if !isJudge {
		return "", false, nil
	}
	total, count, err := judges.QueueCounts(ctx, ev, n.UserID)
	if err != nil {
		return "", false, err
	}
	if n.Kind == hackevents.NoticeJudgingOpen {
		return judgingOpenLine(total, count), true, nil
	}
	line, ok := judgingReminderLine(ev, n.Detail, count, zone)
	return line, ok, nil
}

// judgingOpenLine tells a judge judging opened and what their queue holds: empty, all scored, or count teams still to score.
func judgingOpenLine(total, count int) string {
	switch {
	case total == 0:
		return "Judging is open. No teams are in your queue yet."
	case count == 0:
		return "Judging is open. You've scored every team in your queue."
	}
	return "Judging is open: you have " + teamCount(count) + " to score."
}

// judgingReminderLine writes a judging reminder for count unscored teams.
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

// teamCount is "1 team" or "N teams".
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
	b, err := hackevents.NextMailBatch(ctx, tx, delay, window)
	if errors.Is(err, sql.ErrNoRows) || hackevents.IsMailBatchRaced(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	groups, valid, stale, err := s.groupMailLines(ctx, tx, b, evaluator)
	if err == nil {
		err = hackevents.FinishMailNotices(ctx, tx, stale, "skipped")
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

// mailGroup is one event's part of a notice email.
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
		case n.Kind == hackevents.NoticeApplicationNew:
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
		return hackevents.DeferMailNotices(ctx, tx, valid)
	}
	return hackevents.FinishMailNotices(ctx, tx, valid, "sent")
}

// noticeMailSubject names the one event, or Simple Hack when the mail spans several.
func noticeMailSubject(groups []*mailGroup, kind string) string {
	if len(groups) == 1 {
		return oneLine(groups[0].title) + ": " + noticeSubject(kind)
	}
	return "Simple Hack: " + noticeSubject(kind)
}

// noticeMailBody writes each event's lines (at most 20, then a count) and link, then the footer saying how to turn the mail off.
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

// noticeSubjects is the email subject for each notice kind.
var noticeSubjects = map[string]string{
	hackevents.NoticeJoinRequest:        "someone asked to join your team",
	hackevents.NoticeInvite:             "a team invited you to join",
	hackevents.NoticeRequestAnswered:    "your request to join a team has an answer",
	hackevents.NoticeApplicationNew:     "applications are waiting for you",
	hackevents.NoticeApplicationDecided: "your application has an answer",
	hackevents.NoticeSiteTakenDown:      "your team's site was taken down",
	hackevents.NoticeSiteRestored:       "your team's site is back up",
	hackevents.NoticeDeadlineChanged:    "your team's deadline changed",
	hackevents.TeamAdminNotice:          "you're now a team admin",
	hackevents.NoticeTeamRemoved:        "you were taken off a team",
	hackevents.NoticeEntryReminder:      "your entry is due soon",
	hackevents.NoticeJudgingOpen:        "judging is open",
	hackevents.NoticeJudgingReminder:    "judging closes soon",
	hackevents.NoticeTeamMessage:        "a message from the organisers",
	hackevents.NoticeTeamInbound:        "a team wrote to the organisers",
}

// noticeSubject is the subject for kind, with a generic one for unknown kinds.
func noticeSubject(kind string) string {
	if subject, ok := noticeSubjects[kind]; ok {
		return subject
	}
	return "something is waiting for you"
}

// noticeLink opens the screen a notice kind is about.
func noticeLink(kind, slug, publicBaseURL string) string {
	base := publicBaseURL + "/e/" + slug
	switch kind {
	case hackevents.NoticeApplicationNew:
		return base + "/manage?s=people"
	case hackevents.NoticeApplicationDecided:
		return base + "/team"
	case hackevents.NoticeSiteTakenDown, hackevents.NoticeSiteRestored:
		return base + "/team?s=website"
	case hackevents.NoticeDeadlineChanged, hackevents.NoticeEntryReminder:
		return base + "/team?s=entry"
	case hackevents.NoticeJudgingOpen, hackevents.NoticeJudgingReminder:
		return base + "/judge?s=queue"
	case hackevents.NoticeTeamInbound:
		return base + "/manage?s=teams"
	}
	return base + "/team?s=team"
}

// mailMember is the recipient's membership as a pending notice is re-checked.
type mailMember struct {
	role, status, stage string
	teamID              sql.NullString
	admin               bool
}

// receives reports whether n still concerns this member.
func (m mailMember) receives(n model.MailNotice) bool {
	onTeam := m.teamID.Valid && m.teamID.String == n.TeamID
	if hackevents.NoticeTeamScoped(n.Kind) && !onTeam {
		return false
	}
	finding := m.stage == "open" || m.stage == "building"
	participant := m.role == "participant"
	switch n.Kind {
	case hackevents.NoticeJoinRequest:
		return finding && m.admin && onTeam
	case hackevents.NoticeInvite:
		return finding && participant && !m.teamID.Valid
	case hackevents.NoticeApplicationNew, hackevents.NoticeTeamInbound:
		return m.role == "organiser" && m.stage != "archived"
	case hackevents.NoticeApplicationDecided:
		return participant && m.status == n.Detail
	case hackevents.NoticeRequestAnswered, hackevents.NoticeTeamRemoved, hackevents.NoticeEntryReminder:
		return participant
	}
	return true
}

// mailLine re-checks one pending notice against the state now and writes its line.
func (s *Service) mailLine(ctx context.Context, tx *sql.Tx, n model.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	var m mailMember
	var err error
	m.role, m.status, m.teamID, m.admin, m.stage, err = hackevents.MailMember(ctx, tx, n.EventID, n.UserID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !m.receives(n) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch n.Kind {
	case hackevents.NoticeJoinRequest:
		name, team, err := hackevents.MailRequest(ctx, tx, n.RefID, n.TeamID)
		return lineIfFound(oneLine(name)+" asked to join "+oneLine(team)+".", err)
	case hackevents.NoticeInvite:
		team, err := hackevents.MailInvite(ctx, tx, n.RefID, n.UserID)
		return lineIfFound(oneLine(team)+" invited you to join.", err)
	case hackevents.NoticeApplicationNew:
		pending, err := hackevents.MailApplicationPending(ctx, tx, n.EventID, n.RefID)
		return "", pending && err == nil, err
	case hackevents.NoticeApplicationDecided:
		if n.Detail == "approved" {
			return "Your application was approved. You're in.", true, nil
		}
		return "Your application wasn't accepted this time.", true, nil
	case hackevents.NoticeTeamInbound:
		name, body, err := hackevents.NoticeTeamInboundLine(ctx, tx, n.RefID, n.TeamID)
		return lineIfFound(oneLine(name)+" wrote to the organisers: "+clip(body, 200), err)
	case hackevents.NoticeEntryReminder:
		return s.entryReminderLine(ctx, tx, n, evaluator)
	case hackevents.NoticeJudgingOpen, hackevents.NoticeJudgingReminder:
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

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT"), the way tech_notes asks for ("times in WIB").
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

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or "GMT+5:30" -- the same shape ui.zoneAbbr's gmtOffsetLabel builds.
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

// DeliverNoticeMail sends due notice batches.
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

// PruneNotices removes old notices before a mail sweep.
func (s *Service) PruneNotices(ctx context.Context) (int64, error) {
	n, err := hackevents.PruneNoticesCount(ctx, s.db)
	if err != nil {
		log.Printf("hack: prune notices: %v", err)
	}
	return n, err
}

// MarkNewsSeen dismisses a member's news.
func (s *Service) MarkNewsSeen(ctx context.Context, eventID, userID string, ids []string) (int64, error) {
	return hackevents.MarkNewsSeen(ctx, s.db, eventID, userID, ids)
}

// TodoEvent contains a member's pending work for one event.
type TodoEvent struct {
	Slug, Title, Role string
	Items             []Need
}

// Todos collects pending work and email settings for a user.
func (s *Service) Todos(ctx context.Context, userID string, judges EntryEvaluator) ([]TodoEvent, bool, bool, error) {
	events, roles, err := hackevents.ListEventsForUser(ctx, s.db, userID)
	if err != nil {
		return nil, false, false, err
	}
	out := []TodoEvent{}
	for i, ev := range events {
		m, err := hackevents.GetEventMember(ctx, s.db, ev.ID, userID)
		if err != nil {
			continue
		}
		items := s.NeedsYou(ctx, ev, m, judges)
		if len(items) == 0 {
			continue
		}
		out = append(out, TodoEvent{ev.Slug, ev.Title, roles[i], items})
	}
	on, possible, err := hackevents.EmailUpdates(ctx, s.db, userID)
	return out, on, possible, err
}
