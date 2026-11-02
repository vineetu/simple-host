package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Notify people when something waits on them (2026-11-02, audit item 7).
// What each person sees is computed here, in one place, so the website's
// badges and Needs you lines, GET /v1/hack/todos and the AI apps
// (hack_get_my_todos, hack_get_team_status) always say the same thing.
// Storage and the email queue: internal/db/hack_notices.go.

const (
	noticeMailDelay  = time.Minute      // a notice waits this long first, so an undo sends nothing
	noticeMailWindow = 15 * time.Minute // at most one email per person per kind in this window
	noticeMailBatch  = 50               // emails per sweep
)

func (h *HackHandler) registerNotices(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/todos", wrap(h.getTodos))
	mux.Handle("POST /v1/hack/events/{slug}/notices/seen", wrap(h.markNoticesSeen))
}

// needsYouItem is one line of what waits on the person. id is set for a news
// line (dismissable), count for a live to-do.
func needsYouItem(kind, id string, count int, text, section string, at time.Time) map[string]any {
	item := map[string]any{"kind": kind, "id": nil, "count": count, "text": text, "section": section, "at": nil, "dismissable": id != ""}
	if id != "" {
		item["id"] = id
	}
	if !at.IsZero() {
		item["at"] = at.UTC().Format(time.RFC3339)
	}
	return item
}

// needsYou lists what waits on this member in this event, live to-dos first,
// then unseen news, newest first. Empty for the platform admin reading an
// event it is not in, for a taken-down event, and for a judge.
func (h *HackHandler) needsYou(ctx context.Context, ev db.Event, m db.EventMember) []map[string]any {
	items := []map[string]any{}
	if m.JoinedAt.IsZero() || ev.TakenDown() {
		return items
	}
	finding := ev.TeamFinding && (ev.Stage == "open" || ev.Stage == "building")
	switch m.Role {
	case "organiser":
		if ev.Stage == "archived" {
			return items
		}
		if n, err := db.CountWaitingApplications(ctx, h.database, ev.ID); err == nil && n > 0 {
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
	var status string
	var teamID sql.NullString
	var admin bool
	if err := h.database.QueryRowContext(ctx, `SELECT approval_status, team_id, team_admin FROM event_members WHERE event_id = $1 AND user_id = $2`,
		ev.ID, m.UserID).Scan(&status, &teamID, &admin); err != nil || status != "approved" {
		return items
	}
	if finding && teamID.Valid && admin {
		if reqs, err := db.ListPendingJoinRequests(ctx, h.database, teamID.String); err == nil && len(reqs) > 0 {
			team, _ := db.GetEventTeamByID(ctx, h.database, teamID.String)
			items = append(items, needsYouItem(db.NoticeJoinRequest, "", len(reqs), joinRequestText(reqs, team.Name), "team", time.Time{}))
		}
	}
	if finding && !teamID.Valid {
		if invites, err := db.ListPendingInvites(ctx, h.database, ev.ID, m.UserID); err == nil && len(invites) > 0 {
			text := fmt.Sprintf("%d teams invited you to join.", len(invites))
			if len(invites) == 1 {
				text = invites[0].TeamName + " invited you to join."
			}
			items = append(items, needsYouItem(db.NoticeInvite, "", len(invites), text, "team", time.Time{}))
		}
	}
	news, err := db.ListNews(ctx, h.database, ev.ID, m.UserID)
	if err != nil {
		return items
	}
	for _, n := range news {
		if text, section, ok := h.newsText(ctx, h.database, ev.Title, ev.TimeZone, n); ok {
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
func (h *HackHandler) newsText(ctx context.Context, q db.Querier, eventTitle, zone string, n db.Notice) (string, string, bool) {
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
		var admin bool
		if err := q.QueryRowContext(ctx, `SELECT team_admin FROM event_members WHERE event_id = $1 AND user_id = $2 AND team_id = $3`,
			n.EventID, n.UserID, n.TeamID).Scan(&admin); err != nil || !admin {
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

// getTodos is GET /v1/hack/todos: what waits on the person in every event
// they are in. Events with nothing waiting are left out.
func (h *HackHandler) getTodos(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	events, roles, err := db.ListEventsForUser(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	out := []map[string]any{}
	for i, ev := range events {
		m, err := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
		if err != nil {
			continue
		}
		items := h.needsYou(r.Context(), ev, m)
		if len(items) == 0 {
			continue
		}
		out = append(out, map[string]any{"slug": ev.Slug, "title": ev.Title, "role": roles[i], "items": items})
	}
	on, possible, err := db.EmailUpdates(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "email_updates": on, "email_possible": possible})
}

// markNoticesSeen is POST /v1/hack/events/{slug}/notices/seen: the person
// dismisses their own news lines, the listed ids or all of them.
func (h *HackHandler) markNoticesSeen(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	if a.member.JoinedAt.IsZero() {
		writeEventNotFound(w)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if r.ContentLength != 0 && !decodeHackJSON(w, r, &req) {
		return
	}
	if len(req.IDs) > 50 {
		writeHackErr(w, http.StatusBadRequest, "too_many_ids", "send at most 50 ids")
		return
	}
	for _, id := range req.IDs {
		if !uuidShape.MatchString(id) {
			writeHackErr(w, http.StatusBadRequest, "invalid_id", "ids must be notice ids from needs_you")
			return
		}
	}
	n, err := db.MarkNewsSeen(r.Context(), h.database, a.event.ID, a.user.ID, req.IDs)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": n})
}

// noticeMailPolicy is the sweep's timing; tests shorten it.
type noticeMailPolicy struct{ delay, window time.Duration }

func (h *HackHandler) noticePolicy() noticeMailPolicy {
	if h.noticeTiming != nil {
		return *h.noticeTiming
	}
	return noticeMailPolicy{delay: noticeMailDelay, window: noticeMailWindow}
}

// deliverNoticeMail sends what is due: per person and kind, one email that
// carries everything of that kind still true now, at most one every 15
// minutes. Runs on the content-mail ticker.
func (h *HackHandler) deliverNoticeMail(ctx context.Context) {
	if err := db.PruneNotices(ctx, h.database); err != nil {
		log.Printf("hack: prune notices: %v", err)
	}
	sender, ok := h.mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return
	}
	if ready, ok := h.mailer.(interface{ CanSendNotice() bool }); ok && !ready.CanSendNotice() {
		return
	}
	pol := h.noticePolicy()
	for i := 0; i < noticeMailBatch; i++ {
		more, err := h.sendNoticeBatch(ctx, sender, pol)
		if err != nil {
			log.Printf("hack: notice mail: %v", err)
			return
		}
		if !more {
			return
		}
	}
}

// sendNoticeBatch handles one person and kind. more=false when nothing was due.
func (h *HackHandler) sendNoticeBatch(ctx context.Context, sender interface {
	SendNotice(string, string, string) error
}, pol noticeMailPolicy) (bool, error) {
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	b, err := db.NextMailBatch(ctx, tx, pol.delay, pol.window)
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
		line, ok, err := h.mailLine(ctx, tx, n)
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
		body.WriteString(h.noticeLink(b.Kind, el.slug) + "\n")
	}
	body.WriteString("\nYou get these because your Simple Hack account uses this address. Turn them off on your Account page: " + h.publicBaseURL + "/account\n")
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

func (h *HackHandler) noticeLink(kind, slug string) string {
	base := h.publicBaseURL + "/e/" + slug
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
func (h *HackHandler) mailLine(ctx context.Context, tx *sql.Tx, n db.MailNotice) (string, bool, error) {
	var role, status, stage string
	var teamID sql.NullString
	var admin bool
	err := tx.QueryRowContext(ctx, `
		SELECT m.role, m.approval_status, m.team_id, m.team_admin, e.stage FROM event_members m JOIN events e ON e.id = m.event_id
		 WHERE m.event_id = $1 AND m.user_id = $2 AND e.taken_down_at IS NULL`, n.EventID, n.UserID).
		Scan(&role, &status, &teamID, &admin, &stage)
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
		var name, team string
		err := tx.QueryRowContext(ctx, `
			SELECT m.display_name, t.name FROM event_team_requests r
			  JOIN event_teams t ON t.id = r.team_id
			  JOIN event_members m ON m.event_id = r.event_id AND m.user_id = r.user_id
			 WHERE r.id = $1 AND r.team_id = $2 AND r.kind = 'request'`, n.RefID, n.TeamID).Scan(&name, &team)
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
		var team string
		err := tx.QueryRowContext(ctx, `
			SELECT t.name FROM event_team_requests r JOIN event_teams t ON t.id = r.team_id
			 WHERE r.id = $1 AND r.user_id = $2 AND r.kind = 'invite'`, n.RefID, n.UserID).Scan(&team)
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
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2
			AND role = 'participant' AND approval_status = 'pending')`, n.EventID, n.RefID).Scan(&pending); err != nil {
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
		return h.entryReminderLine(ctx, tx, n)
	}
	text, _, ok := h.newsText(ctx, tx, n.EventTitle, n.EventTimeZone, n.Notice)
	return oneLine(text), ok, nil
}
