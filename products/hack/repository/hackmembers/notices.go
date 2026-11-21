package hackmembers

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Notices: notify people when something waits on them.
const (
	NoticeJoinRequest        = "join_request"
	NoticeInvite             = "invite"
	NoticeRequestAnswered    = "request_answered"
	NoticeApplicationNew     = "application_new"
	NoticeApplicationDecided = "application_decided"
	NoticeSiteTakenDown      = "site_taken_down"
	NoticeSiteRestored       = "site_restored"
	NoticeDeadlineChanged    = "deadline_changed"
	TeamAdminNotice          = "team_admin"
	NoticeTeamLocked         = "team_locked"
	NoticeTeamRemoved        = "team_removed"
	NoticeEntryReminder      = "entry_reminder"
	NoticeJudgingOpen        = "judging_open"
	NoticeJudgingReminder    = "judging_reminder"
	NoticeTeamMessage        = "team_message"
	// A team member wrote to the organisers; goes to every organiser of the event.
	NoticeTeamInbound = "team_inbound"
)

// noticeIsNews: shown in the app as a dismissable line until seen.
func noticeIsNews(kind string) bool {
	switch kind {
	case NoticeRequestAnswered, NoticeApplicationDecided, NoticeSiteTakenDown, NoticeSiteRestored,
		NoticeDeadlineChanged, TeamAdminNotice, NoticeTeamLocked,
		NoticeJudgingOpen, NoticeJudgingReminder, NoticeTeamMessage, NoticeTeamInbound:
		return true
	}
	return false
}

// noticeEmails: every kind but a team lock, which has nothing to act on.
func noticeEmails(kind string) bool { return kind != NoticeTeamLocked }

// NoticeTeamScoped kinds describe the person's current team.
func NoticeTeamScoped(kind string) bool {
	switch kind {
	case NoticeSiteTakenDown, NoticeSiteRestored, NoticeDeadlineChanged, TeamAdminNotice, NoticeTeamLocked,
		NoticeEntryReminder, NoticeTeamMessage:
		return true
	}
	return false
}

func NotifyUsers(ctx context.Context, q sqlkit.Querier, eventID, kind, teamID, refID, detail string, userIDs ...string) error {
	_, err := NotifyUsersCount(ctx, q, eventID, kind, teamID, refID, detail, userIDs...)
	return err
}

func NotifyUsersCount(ctx context.Context, q sqlkit.Querier, eventID, kind, teamID, refID, detail string, userIDs ...string) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	news, mail := noticeIsNews(kind), noticeEmails(kind)
	users, err := accounts.UsersByIDs(ctx, q, userIDs)
	if err != nil {
		return 0, err
	}
	ids, mailable := make([]string, 0, len(users)), []string{}
	for id := range users {
		ids = append(ids, id)
	}
	if mail {
		if mailable, err = emailableIDs(ctx, q, users); err != nil {
			return 0, err
		}
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO event_notices (event_id, user_id, kind, team_id, ref_id, detail, seen_at, email_state)
		SELECT $1, u.id, $3, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6,
		       CASE WHEN $7 THEN NULL ELSE now() END,
		       CASE WHEN u.id = ANY($8::uuid[]) THEN 'pending' ELSE 'none' END
		  FROM unnest($2::uuid[]) AS u(id)
		 WHERE ($7 OR u.id = ANY($8::uuid[]))
		   AND NOT EXISTS (SELECT 1 FROM event_notices x
		         WHERE x.user_id = u.id AND x.event_id = $1 AND x.kind = $3
		           AND x.team_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid
		           AND x.ref_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid
		           AND x.detail = $6 AND x.email_state = 'pending')`,
		eventID, pq.Array(ids), kind, teamID, refID, detail, news, pq.Array(mailable))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NotifyMembers notifies everyone f picks.
func NotifyMembers(ctx context.Context, q sqlkit.Querier, f Filter, kind, teamID, refID, detail string) error {
	ids, err := IDs(ctx, q, f)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, f.EventID, kind, teamID, refID, detail, ids...)
}

// NotifyTeamAdmins notifies the team's admins.
func NotifyTeamAdmins(ctx context.Context, q sqlkit.Querier, eventID, teamID, kind, refID, detail string) error {
	return NotifyMembers(ctx, q, Filter{EventID: eventID, TeamID: teamID, TeamAdmin: true}, kind, teamID, refID, detail)
}

// NotifyTeamMembers notifies the team's members but except.
func NotifyTeamMembers(ctx context.Context, q sqlkit.Querier, eventID, teamID, kind, detail, except string) error {
	return NotifyMembers(ctx, q, Filter{EventID: eventID, TeamID: teamID, Except: except}, kind, teamID, "", detail)
}

// NotifyOrganisers notifies every organiser of the event.
func NotifyOrganisers(ctx context.Context, q sqlkit.Querier, eventID, kind, refID string) error {
	return NotifyMembers(ctx, q, Filter{EventID: eventID, Role: "organiser"}, kind, "", refID, "")
}

// NotifyJudgingOpen keeps one undismissed opening notice when judging reopens.
func NotifyJudgingOpen(ctx context.Context, q sqlkit.Querier, eventID string) error {
	kind := NoticeJudgingOpen
	ids, err := ids(ctx, q, `SELECT m.user_id FROM event_members m
		WHERE m.event_id = $1 AND m.role = 'judge'
		AND NOT EXISTS (SELECT 1 FROM event_notices n
			WHERE n.event_id = m.event_id AND n.user_id = m.user_id
			AND n.kind = $2 AND n.seen_at IS NULL)`, eventID, kind)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, eventID, kind, "", "", "", ids...)
}

// SupersedeTeamNotices marks earlier notices of these kinds about the team seen, and drops their unsent emails.
func SupersedeTeamNotices(ctx context.Context, q sqlkit.Querier, teamID, userID string, kinds ...string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_notices
		   SET seen_at = COALESCE(seen_at, now()),
		       email_state = CASE WHEN email_state = 'pending' THEN 'skipped' ELSE email_state END
		 WHERE team_id = $1 AND kind = ANY($2)
		   AND (NULLIF($3,'')::uuid IS NULL OR user_id = NULLIF($3,'')::uuid)
		   AND (seen_at IS NULL OR email_state = 'pending')`, teamID, pq.Array(kinds), userID)
	return err
}

// TeamMembersWhoKnew is the team's members but except with no open notice of these kinds about it.
func TeamMembersWhoKnew(ctx context.Context, q sqlkit.Querier, eventID, teamID, except string, kinds ...string) ([]string, error) {
	return ids(ctx, q, `
		SELECT m.user_id FROM event_members m
		 WHERE m.event_id = $1 AND m.team_id = $2 AND m.user_id::text <> $3
		   AND NOT EXISTS (SELECT 1 FROM event_notices n
		         WHERE n.user_id = m.user_id AND n.team_id = $2 AND n.kind = ANY($4)
		           AND n.seen_at IS NULL AND n.email_state IN ('none','pending','skipped'))`,
		eventID, teamID, except, pq.Array(kinds))
}

type Notice = model.Notice

// ListNews is the person's unseen news lines in one event, newest first.
func ListNews(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]Notice, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT n.id, n.event_id, n.user_id, n.kind, COALESCE(n.team_id::text,''), COALESCE(n.ref_id::text,''), n.detail, n.created_at
		  FROM event_notices n
		  JOIN event_members m ON m.event_id = n.event_id AND m.user_id = n.user_id
		 WHERE n.event_id = $1 AND n.user_id = $2 AND n.seen_at IS NULL
		   AND n.created_at > now() - interval '14 days'
		   AND (n.kind NOT IN ('site_taken_down','site_restored','deadline_changed','team_admin','team_locked','team_message')
		        OR n.team_id = m.team_id)
		 ORDER BY n.created_at DESC, n.id LIMIT 20`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.EventID, &n.UserID, &n.Kind, &n.TeamID, &n.RefID, &n.Detail, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNewsSeen marks the person's own news in one event seen: ids, or every line when ids is empty.
func MarkNewsSeen(ctx context.Context, q sqlkit.Querier, eventID, userID string, ids []string) (int64, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE event_notices SET seen_at = now()
		 WHERE event_id = $1 AND user_id = $2 AND seen_at IS NULL AND kind <> 'team_inbound'
		   AND (cardinality($3::uuid[]) = 0 OR id = ANY($3::uuid[]))`, eventID, userID, pq.Array(ids))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InboundGroup is one team's unread messages to an organiser.
type InboundGroup = model.InboundGroup

// InboundGroups is the organiser's unread team_inbound notices, one group per team, newest first.
func InboundGroups(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]InboundGroup, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT team_id::text, count(*),
		       COALESCE((array_agg(ref_id::text ORDER BY created_at DESC, id DESC))[1], ''), max(created_at)
		  FROM event_notices
		 WHERE event_id = $1 AND user_id = $2 AND kind = 'team_inbound' AND seen_at IS NULL AND team_id IS NOT NULL
		 GROUP BY team_id ORDER BY max(created_at) DESC`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboundGroup{}
	for rows.Next() {
		var g InboundGroup
		if err := rows.Scan(&g.TeamID, &g.Count, &g.LatestRef, &g.LatestAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// MarkInboundSeen marks the organiser's unread notices about these messages of one team seen.
func MarkInboundSeen(ctx context.Context, q sqlkit.Querier, eventID, userID, teamID string, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	_, err := q.ExecContext(ctx, `
		UPDATE event_notices SET seen_at = now()
		 WHERE event_id = $1 AND user_id = $2 AND team_id = $3 AND kind = 'team_inbound' AND seen_at IS NULL
		   AND ref_id = ANY($4::uuid[])`, eventID, userID, teamID, pq.Array(messageIDs))
	return err
}

// CountWaitingApplications counts every participant waiting for an organiser's decision, by application or by a join link that needs approval.
func CountWaitingApplications(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	return Count(ctx, q, Filter{EventID: eventID, Role: "participant", Status: "pending"})
}

// MailBatch is one person's pending notices of one kind, locked until the caller commits.
type MailBatch = model.MailBatch

// MailNotice is a pending notice with its event, for the email text.
type MailNotice = model.MailNotice

// liveNoticeEvents is the events of pending notices that still exist
// (event_notices has no foreign key to events).
func liveNoticeEvents(ctx context.Context, q sqlkit.Querier, query string, args ...any) (map[string]hackevents.Event, error) {
	eventIDs, err := ids(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	return hackevents.EventsByIDs(ctx, q, eventIDs)
}

func keys(m map[string]hackevents.Event) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// NextMailBatch picks the oldest person and kind with a notice that waited at least delay and no email of that kind in the last window, takes an advisory lock on that person and kind.
func NextMailBatch(ctx context.Context, tx *sql.Tx, delay, window time.Duration) (MailBatch, error) {
	var b MailBatch
	secs := func(d time.Duration) float64 { return d.Seconds() }
	live, err := liveNoticeEvents(ctx, tx, `SELECT DISTINCT event_id::text FROM event_notices WHERE email_state = 'pending'`)
	if err != nil {
		return b, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT n.user_id, n.kind FROM event_notices n
		 WHERE n.email_state = 'pending' AND n.event_id = ANY($3::uuid[]) AND n.created_at <= now() - make_interval(secs => $1)
		   AND (n.attempted_at IS NULL OR n.attempted_at <= now() - interval '5 minutes')
		   AND NOT EXISTS (SELECT 1 FROM event_notices s WHERE s.user_id = n.user_id AND s.kind = n.kind
		         AND s.email_state = 'sent' AND s.emailed_at > now() - make_interval(secs => $2))
		 ORDER BY n.created_at, n.id LIMIT 1 FOR UPDATE SKIP LOCKED`, secs(delay), secs(window), pq.Array(keys(live))).Scan(&b.UserID, &b.Kind)
	if err != nil {
		return b, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1 || ':' || $2, 7311))`, b.UserID, b.Kind); err != nil {
		return b, err
	}
	var recent bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_notices WHERE user_id = $1 AND kind = $2
		AND email_state = 'sent' AND emailed_at > now() - make_interval(secs => $3))`, b.UserID, b.Kind, secs(window)).Scan(&recent); err != nil {
		return b, err
	}
	if recent {
		// Another sweeper sent this kind while we waited for the lock.
		return b, errMailBatchRaced
	}
	users, err := accounts.UsersByIDs(ctx, tx, []string{b.UserID})
	if err != nil {
		return b, err
	}
	mailable, err := emailableIDs(ctx, tx, users)
	if err != nil {
		return b, err
	}
	if len(mailable) == 1 {
		b.Recipient = users[mailable[0]].Username
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.event_id, n.user_id, n.kind, COALESCE(n.team_id::text,''), COALESCE(n.ref_id::text,''), n.detail, n.created_at
		  FROM event_notices n
		 WHERE n.user_id = $1 AND n.kind = $2 AND n.email_state = 'pending'
		   AND n.created_at <= now() - make_interval(secs => $3)
		 ORDER BY n.created_at, n.id FOR UPDATE OF n SKIP LOCKED`, b.UserID, b.Kind, secs(delay))
	if err != nil {
		return b, err
	}
	defer rows.Close()
	var found []MailNotice
	for rows.Next() {
		var m MailNotice
		if err := rows.Scan(&m.ID, &m.EventID, &m.UserID, &m.Kind, &m.TeamID, &m.RefID, &m.Detail, &m.CreatedAt); err != nil {
			return b, err
		}
		found = append(found, m)
	}
	if err := rows.Err(); err != nil {
		return b, err
	}
	events, err := hackevents.EventsByIDs(ctx, tx, noticeEventIDs(found))
	if err != nil {
		return b, err
	}
	for _, m := range found {
		e, ok := events[m.EventID]
		if !ok {
			continue
		}
		m.EventSlug, m.EventTitle, m.EventTimeZone, m.EventStage, m.EventTakenDown = e.Slug, e.Title, e.TimeZone, e.Stage, e.TakenDown()
		b.Notices = append(b.Notices, m)
	}
	// Grouped by event (its title, then id), oldest first within one.
	sort.SliceStable(b.Notices, func(i, j int) bool {
		x, y := b.Notices[i], b.Notices[j]
		if x.EventTitle != y.EventTitle {
			return x.EventTitle < y.EventTitle
		}
		return x.EventID < y.EventID
	})
	if len(b.Notices) > 200 {
		b.Notices = b.Notices[:200]
	}
	return b, nil
}

func noticeEventIDs(ns []MailNotice) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range ns {
		if !seen[n.EventID] {
			seen[n.EventID] = true
			out = append(out, n.EventID)
		}
	}
	return out
}

var errMailBatchRaced = errors.New("notice mail: another sweeper sent this kind")

// IsMailBatchRaced reports NextMailBatch's lost race (nothing to do this time).
func IsMailBatchRaced(err error) bool { return errors.Is(err, errMailBatchRaced) }

// FinishMailNotices sets the email state of these notices: sent (with emailed_at, which starts the person's window for this kind) or skipped.
func FinishMailNotices(ctx context.Context, tx *sql.Tx, ids []string, state string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE event_notices SET email_state = $2,
		emailed_at = CASE WHEN $2 = 'sent' THEN now() ELSE emailed_at END WHERE id = ANY($1::uuid[])`, pq.Array(ids), state)
	return err
}

// DeferMailNotices leaves failed notices pending, retried after 5 minutes.
func DeferMailNotices(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE event_notices SET attempted_at = now() WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	return err
}

// PendingMail counts the notices waiting to be emailed, delayed retries included.
func PendingMail(ctx context.Context, q sqlkit.Querier) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_notices WHERE email_state='pending'`).Scan(&n)
	return n, err
}

// PruneNotices deletes notices older than 30 days, seen or not, and any whose event is gone (event_notices has no foreign key to events).
func PruneNotices(ctx context.Context, q sqlkit.Querier) error {
	_, err := PruneNoticesCount(ctx, q)
	return err
}

func PruneNoticesCount(ctx context.Context, q sqlkit.Querier) (int64, error) {
	eventIDs, err := ids(ctx, q, `SELECT DISTINCT event_id::text FROM event_notices`)
	if err != nil {
		return 0, err
	}
	live, err := hackevents.EventsByIDs(ctx, q, eventIDs)
	if err != nil {
		return 0, err
	}
	gone := []string{}
	for _, id := range eventIDs {
		if _, ok := live[id]; !ok {
			gone = append(gone, id)
		}
	}
	res, err := q.ExecContext(ctx, `DELETE FROM event_notices WHERE created_at < now() - interval '30 days'
		OR event_id = ANY($1::uuid[])`, pq.Array(gone))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DismissTeamMessages clears stage-specific organiser messages when the stage changes.
func DismissTeamMessages(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `UPDATE event_notices SET seen_at=now() WHERE event_id=$1 AND kind='team_message' AND seen_at IS NULL`, eventID)
	return err
}

// DeleteNotices drops everything queued for one person in one event (they left).
func DeleteNotices(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_notices WHERE event_id=$1 AND user_id=$2`, eventID, userID)
	return err
}

// DeleteEventNotices drops the event's notices (no foreign key cascades them).
func DeleteEventNotices(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_notices WHERE event_id = $1`, eventID)
	return err
}
