package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
	"log"
	"regexp"
	"strings"
	"time"
)

// Need is one line of work or news awaiting a member. ID marks dismissable news; Count is for a live to-do.
type Need struct {
	Kind, ID, Text, Section string
	Count                   int
	At                      time.Time
}

func needsYouItem(kind, id string, count int, text, section string, at time.Time) Need {
	return Need{kind, id, text, section, count, at}
}

// NeedsYou lists what waits on this member in this event, live to-dos first,
// then unseen news, newest first. Empty for the platform admin reading an
// event it is not in, for a taken-down event, and for a judge.
func (s *Service) NeedsYou(ctx context.Context, ev db.Event, m db.EventMember) []Need {
	items := []Need{}
	if m.JoinedAt.IsZero() || ev.TakenDown() {
		return items
	}
	finding := ev.TeamFinding && (ev.Stage == "open" || ev.Stage == "building")
	switch m.Role {
	case "organiser":
		if ev.Stage == "archived" {
			return items
		}
		if n, err := db.CountWaitingApplications(ctx, s.db, ev.ID); err == nil && n > 0 {
			text := fmt.Sprintf("%d applications are waiting for a decision.", n)
			if n == 1 {
				text = "1 application is waiting for a decision."
			}
			items = append(items, needsYouItem(db.NoticeApplicationNew, "", n, text, "people", time.Time{}))
		}
		return items
	case "participant":
	default:
		return items
	}
	status, teamID, admin, err := hackevents.NoticeMemberStatus(ctx, s.db, ev.ID, m.UserID)
	if err != nil || status != "approved" {
		return items
	}
	if finding && teamID.Valid && admin {
		if reqs, err := db.ListPendingJoinRequests(ctx, s.db, teamID.String); err == nil && len(reqs) > 0 {
			team, _ := db.GetEventTeamByID(ctx, s.db, teamID.String)
			items = append(items, needsYouItem(db.NoticeJoinRequest, "", len(reqs), joinRequestText(reqs, team.Name), "team", time.Time{}))
		}
	}
	if finding && !teamID.Valid {
		if invites, err := db.ListPendingInvites(ctx, s.db, ev.ID, m.UserID); err == nil && len(invites) > 0 {
			text := fmt.Sprintf("%d teams invited you to join.", len(invites))
			if len(invites) == 1 {
				text = invites[0].TeamName + " invited you to join."
			}
			items = append(items, needsYouItem(db.NoticeInvite, "", len(invites), text, "team", time.Time{}))
		}
	}
	news, err := db.ListNews(ctx, s.db, ev.ID, m.UserID)
	if err != nil {
		return items
	}
	for _, n := range news {
		if text, section, ok := s.newsText(ctx, s.db, ev.Title, ev.TimeZone, n); ok {
			items = append(items, needsYouItem(n.Kind, n.ID, 1, text, section, n.CreatedAt))
		}
	}
	return items
}

func joinRequestText(reqs []db.PendingJoinRequest, team string) string {
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

// newsText is the line for one news notice, from the team's state now (a
// renamed team reads with its new name), and its screen. ok=false when the
// notice no longer holds: a take-down already reversed, admin since removed,
// a team since unlocked or gone.
func (s *Service) newsText(ctx context.Context, q db.Querier, eventTitle, zone string, n db.Notice) (string, string, bool) {
	var team db.EventTeam
	if n.TeamID != "" {
		t, err := db.GetEventTeamByID(ctx, q, n.TeamID)
		if err != nil && db.NoticeTeamScoped(n.Kind) {
			return "", "", false
		}
		team = t
	}
	teamName := team.Name
	if teamName == "" {
		teamName = "The team"
	}
	switch n.Kind {
	case db.NoticeRequestAnswered:
		if n.Detail == "accepted" {
			return teamName + " accepted your request. You're on the team.", "team", true
		}
		return teamName + " said no to your request to join.", "team", true
	case db.NoticeApplicationDecided:
		// A rejection shows as the rejected card instead, never as a line.
		return "Your application was approved. Welcome to " + eventTitle + ".", "home", n.Detail == "approved"
	case db.NoticeSiteTakenDown:
		if !team.SiteTakenDownAt.Valid {
			return "", "", false
		}
		text := "The organisers took your team's site down."
		if r := oneLine(team.SiteTakenDownReason); r != "" {
			text += " Their reason: " + r
		}
		return text, "website", true
	case db.NoticeSiteRestored:
		if team.SiteTakenDownAt.Valid {
			return "", "", false
		}
		return "The organisers put your team's site back up.", "website", true
	case db.NoticeDeadlineChanged:
		st, err := db.TeamWriteStateFor(ctx, q, team.ID, false)
		if err != nil {
			return "", "", false
		}
		if st.HasOverride && st.Deadline.Valid {
			return "Your team has until " + hackNoticeTime(st.Deadline.Time, zone) + " to finish its entry.", "entry", true
		}
		text := "Your team's extra time was taken away."
		if st.Deadline.Valid {
			text += " Entries close " + hackNoticeTime(st.Deadline.Time, zone) + "."
		}
		return text, "entry", true
	case db.NoticeTeamAdmin:
		admin, err := hackevents.NoticeTeamAdmin(ctx, q, n.EventID, n.UserID, n.TeamID)
		if err != nil || !admin {
			return "", "", false
		}
		return "You're now an admin of " + team.Name + ". You answer requests to join and can lock the team.", "team", true
	case db.NoticeTeamLocked:
		if !team.Locked() {
			return "", "", false
		}
		return team.Name + " is locked, so nobody new can join.", "team", true
	case db.NoticeTeamRemoved:
		return oneLine(n.Detail), "team", n.Detail != ""
	}
	return "", "", false
}

// sendNoticeBatch handles one person and kind. more=false when nothing was due.
func (s *Service) sendNoticeBatch(ctx context.Context, sender interface {
	SendNotice(string, string, string) error
}, delay, window time.Duration, publicBaseURL string, evaluator EntryEvaluator) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	b, err := db.NextMailBatch(ctx, tx, delay, window)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if db.IsMailBatchRaced(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var valid, stale []string
	type eventLines struct {
		slug, title string
		lines       []string
		apps        int
	}
	var order []string
	byEvent := map[string]*eventLines{}
	for _, n := range b.Notices {
		line, ok, err := s.mailLine(ctx, tx, n, evaluator)
		if err != nil {
			return false, err
		}
		if !ok || b.Recipient == "" {
			stale = append(stale, n.ID)
			continue
		}
		valid = append(valid, n.ID)
		el := byEvent[n.EventID]
		if el == nil {
			el = &eventLines{slug: n.EventSlug, title: n.EventTitle}
			byEvent[n.EventID] = el
			order = append(order, n.EventID)
		}
		if n.Kind == db.NoticeApplicationNew {
			el.apps++
		} else if !containsString(el.lines, line) {
			el.lines = append(el.lines, line)
		}
	}
	if err := db.FinishMailNotices(ctx, tx, stale, "skipped"); err != nil {
		return false, err
	}
	if len(valid) == 0 {
		return true, tx.Commit()
	}
	var body strings.Builder
	for i, id := range order {
		el := byEvent[id]
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(oneLine(el.title) + "\n")
		if el.apps > 0 {
			if el.apps == 1 {
				body.WriteString("1 new application is waiting for a decision.\n")
			} else {
				fmt.Fprintf(&body, "%d new applications are waiting for a decision.\n", el.apps)
			}
		}
		for j, line := range el.lines {
			if j == 20 {
				fmt.Fprintf(&body, "And %d more.\n", len(el.lines)-20)
				break
			}
			body.WriteString(line + "\n")
		}
		body.WriteString(s.noticeLink(b.Kind, el.slug, publicBaseURL) + "\n")
	}
	body.WriteString("\nYou get these because your Simple Hack account uses this address. Turn them off on your Account page: " + publicBaseURL + "/account\n")
	subject := noticeSubject(b.Kind)
	if len(order) == 1 {
		subject = oneLine(byEvent[order[0]].title) + ": " + subject
	} else {
		subject = "Simple Hack: " + subject
	}
	if err := sender.SendNotice(b.Recipient, subject, body.String()); err != nil {
		log.Printf("hack: notice mail delivery failed: %v", err)
		if err := db.DeferMailNotices(ctx, tx, valid); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if err := db.FinishMailNotices(ctx, tx, valid, "sent"); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func noticeSubject(kind string) string {
	switch kind {
	case db.NoticeJoinRequest:
		return "someone asked to join your team"
	case db.NoticeInvite:
		return "a team invited you to join"
	case db.NoticeRequestAnswered:
		return "your request to join a team has an answer"
	case db.NoticeApplicationNew:
		return "applications are waiting for you"
	case db.NoticeApplicationDecided:
		return "your application has an answer"
	case db.NoticeSiteTakenDown:
		return "your team's site was taken down"
	case db.NoticeSiteRestored:
		return "your team's site is back up"
	case db.NoticeDeadlineChanged:
		return "your team's deadline changed"
	case db.NoticeTeamAdmin:
		return "you're now a team admin"
	case db.NoticeTeamRemoved:
		return "you were taken off a team"
	case db.NoticeEntryReminder:
		return "your entry is due soon"
	}
	return "something is waiting for you"
}

func (s *Service) noticeLink(kind, slug, publicBaseURL string) string {
	base := publicBaseURL + "/e/" + slug
	switch kind {
	case db.NoticeApplicationNew:
		return base + "/manage?s=people"
	case db.NoticeApplicationDecided:
		return base + "/team"
	case db.NoticeSiteTakenDown, db.NoticeSiteRestored:
		return base + "/team?s=website"
	case db.NoticeDeadlineChanged, db.NoticeEntryReminder:
		return base + "/team?s=entry"
	}
	return base + "/team?s=team"
}

// mailLine re-checks one pending notice against the state now and writes
// its line. ok=false drops it: the request was answered or withdrawn, the
// recipient is no longer that team's admin or an organiser, the application
// was decided, the person left the event or the team, the event was taken
// down. A join request names the person asking only to that team's current
// admins; an application never names the applicant.
func (s *Service) mailLine(ctx context.Context, tx *sql.Tx, n db.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	role, status, teamID, admin, stage, err := hackevents.MailMember(ctx, tx, n.EventID, n.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	onTeam := teamID.Valid && teamID.String == n.TeamID
	if db.NoticeTeamScoped(n.Kind) && !onTeam {
		return "", false, nil
	}
	finding := stage == "open" || stage == "building"
	switch n.Kind {
	case db.NoticeJoinRequest:
		if !finding || !admin || !onTeam {
			return "", false, nil
		}
		name, team, err := hackevents.MailRequest(ctx, tx, n.RefID, n.TeamID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return oneLine(name) + " asked to join " + oneLine(team) + ".", true, nil
	case db.NoticeInvite:
		if !finding || teamID.Valid || role != "participant" {
			return "", false, nil
		}
		team, err := hackevents.MailInvite(ctx, tx, n.RefID, n.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return oneLine(team) + " invited you to join.", true, nil
	case db.NoticeApplicationNew:
		if role != "organiser" || stage == "archived" {
			return "", false, nil
		}
		pending, err := hackevents.MailApplicationPending(ctx, tx, n.EventID, n.RefID)
		if err != nil {
			return "", false, err
		}
		return "", pending, nil
	case db.NoticeApplicationDecided:
		if role != "participant" || status != n.Detail {
			return "", false, nil
		}
		if n.Detail == "approved" {
			return "Your application was approved. You're in.", true, nil
		}
		return "Your application wasn't accepted this time.", true, nil
	case db.NoticeRequestAnswered, db.NoticeTeamRemoved:
		if role != "participant" {
			return "", false, nil
		}
	case db.NoticeEntryReminder:
		if role != "participant" {
			return "", false, nil
		}
		return s.entryReminderLine(ctx, tx, n, evaluator)
	}
	text, _, ok := s.newsText(ctx, tx, n.EventTitle, n.EventTimeZone, n.Notice)
	return oneLine(text), ok, nil
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

// hackZoneAbbrFamiliar is the same fixed map ui.zoneAbbr (internal/handler/
// static/hack/ui.js) and the vote page's own copy of it use -- kept in step
// by hand, since Go's tzdata and Intl don't always agree on which zones get
// a real short code. Go already gets North America, the UK and Central
// Europe (CET/CEST) and Australia (AEDT/AEST) right from tzdata directly;
// this map only covers the zones tzdata leaves as a bare numeric offset.
var hackZoneAbbrFamiliar = map[string]string{
	"Asia/Jakarta": "WIB", "Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
	"Asia/Tokyo":     "JST",
	"Asia/Seoul":     "KST",
	"Asia/Singapore": "SGT",
	"Asia/Kolkata":   "IST", "Asia/Calcutta": "IST",
}

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT"),
// the way tech_notes asks for ("times in WIB"). Go's tzdata does not assign
// every zone a short code; when what comes back looks like a bare UTC offset
// ("+07") this checks the familiar-zone map first (SGT, WIB, WITA, WIT, JST,
// KST, IST -- same table and same decision as the app's ui.zoneAbbr and the
// vote page), then falls back to a GMT±N label built from the numeric
// offset itself, never the IANA zone name ("Asia/Singapore") -- consistent
// with what a browser without a named abbreviation for that zone shows.
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

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or
// "GMT+5:30" -- the same shape ui.zoneAbbr's gmtOffsetLabel builds.
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
func (s *Service) DeliverNoticeMail(ctx context.Context, sender NoticeSender, delay, window time.Duration, publicBaseURL string, evaluator EntryEvaluator) {
	for i := 0; i < 50; i++ {
		more, err := s.sendNoticeBatch(ctx, sender, delay, window, publicBaseURL, evaluator)
		if err != nil {
			log.Printf("hack: notice mail: %v", err)
			return
		}
		if !more {
			return
		}
	}
}

// PruneNotices removes old notices before a mail sweep.
func (s *Service) PruneNotices(ctx context.Context) {
	if err := db.PruneNotices(ctx, s.db); err != nil {
		log.Printf("hack: prune notices: %v", err)
	}
}

// MarkNewsSeen dismisses a member's news.
func (s *Service) MarkNewsSeen(ctx context.Context, eventID, userID string, ids []string) (int64, error) {
	return db.MarkNewsSeen(ctx, s.db, eventID, userID, ids)
}

// TodoEvent contains a member's pending work for one event.
type TodoEvent struct {
	Slug, Title, Role string
	Items             []Need
}

// Todos collects pending work and email settings for a user.
func (s *Service) Todos(ctx context.Context, userID string) ([]TodoEvent, bool, bool, error) {
	events, roles, err := db.ListEventsForUser(ctx, s.db, userID)
	if err != nil {
		return nil, false, false, err
	}
	out := []TodoEvent{}
	for i, ev := range events {
		m, err := db.GetEventMember(ctx, s.db, ev.ID, userID)
		if err != nil {
			continue
		}
		items := s.NeedsYou(ctx, ev, m)
		if len(items) == 0 {
			continue
		}
		out = append(out, TodoEvent{ev.Slug, ev.Title, roles[i], items})
	}
	on, possible, err := db.EmailUpdates(ctx, s.db, userID)
	return out, on, possible, err
}
