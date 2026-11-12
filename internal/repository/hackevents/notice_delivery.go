package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// Notices: "notify people when something waits on them" (2026-11-02, audit
// item 7, db/migrations/zw-hack-wait-notices.sql). One event_notices row per
// person per thing that happened. The row is the in-app news line (seen_at)
// and the email queue (email_state) at once. The live to-dos (a join request
// waiting for a team's admins, an invite, an application waiting for the
// organisers) are read from current state instead (ListLiveTodos), so they
// vanish the moment they are answered; their rows exist only to be emailed.
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
	// 0003-hack-notices-judges-messages.sql: judging_open/judging_reminder go
	// to every current judge member (not team-scoped); team_message goes to
	// one team's current members.
	NoticeJudgingOpen     = "judging_open"
	NoticeJudgingReminder = "judging_reminder"
	NoticeTeamMessage     = "team_message"
	// 0015-hack-team-to-organiser-messages.sql: a team member wrote to the
	// organisers; goes to every organiser of the event.
	NoticeTeamInbound = "team_inbound"
)

// noticeIsNews: shown in the app as a dismissable line until seen. The other
// kinds are either live to-dos or, for team_removed, already shown by the
// member's own team_removal_notice, so their rows are born seen.
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

// NoticeTeamScoped kinds describe the person's current team. They are shown
// and emailed only while the person is still on that team, so a line about a
// team never reaches someone who has since moved to another one.
func NoticeTeamScoped(kind string) bool {
	switch kind {
	case NoticeSiteTakenDown, NoticeSiteRestored, NoticeDeadlineChanged, TeamAdminNotice, NoticeTeamLocked,
		NoticeEntryReminder, NoticeTeamMessage:
		return true
	}
	return false
}

// emailableSQL is the one rule for who may be emailed, used both when a
// notice is queued and again at send time: a sign-in name that is an email
// address (key-only accounts have none and never get mail), not an event's
// holding account, not suspended, and "Email me when something waits on me"
// left on. u is users, p is a LEFT JOINed hack_account_preferences.
const emailableSQL = `(u.username LIKE '%@%' AND NOT u.event_account AND u.suspended_at IS NULL AND COALESCE(p.email_updates, true))`

// NotifyUsers queues one notice of kind for each user. teamID, refID and
// detail may be empty. A row is written only when it will show in the app or
// can be emailed. An identical notice still waiting to be emailed is not
// queued twice, so asking, withdrawing and asking again inside the email
// window costs one line, not three.
func NotifyUsers(ctx context.Context, q sqlkit.Querier, eventID, kind, teamID, refID, detail string, userIDs ...string) error {
	_, err := NotifyUsersCount(ctx, q, eventID, kind, teamID, refID, detail, userIDs...)
	return err
}

func NotifyUsersCount(ctx context.Context, q sqlkit.Querier, eventID, kind, teamID, refID, detail string, userIDs ...string) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	news, mail := noticeIsNews(kind), noticeEmails(kind)
	res, err := q.ExecContext(ctx, `
		INSERT INTO event_notices (event_id, user_id, kind, team_id, ref_id, detail, seen_at, email_state)
		SELECT $1, u.id, $3, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6,
		       CASE WHEN $7 THEN NULL ELSE now() END,
		       CASE WHEN $8 AND `+emailableSQL+` THEN 'pending' ELSE 'none' END
		  FROM users u LEFT JOIN hack_account_preferences p ON p.user_id = u.id
		 WHERE u.id = ANY($2::uuid[])
		   AND ($7 OR ($8 AND `+emailableSQL+`))
		   AND NOT EXISTS (SELECT 1 FROM event_notices x
		         WHERE x.user_id = u.id AND x.event_id = $1 AND x.kind = $3
		           AND x.team_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid
		           AND x.ref_id IS NOT DISTINCT FROM NULLIF($5,'')::uuid
		           AND x.detail = $6 AND x.email_state = 'pending')`,
		eventID, pq.Array(userIDs), kind, teamID, refID, detail, news, mail)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NotifyTeamAdmins queues kind for every current admin of the team.
func NotifyTeamAdmins(ctx context.Context, q sqlkit.Querier, eventID, teamID, kind, refID, detail string) error {
	ids, err := userIDs(ctx, q, `SELECT user_id FROM event_members WHERE event_id = $1 AND team_id = $2 AND team_admin`, eventID, teamID)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, eventID, kind, teamID, refID, detail, ids...)
}

// TeamMemberIDs lists the team's members, leaving out except (the person who
// made the change, who needs no notice of it).
func TeamMemberIDs(ctx context.Context, q sqlkit.Querier, eventID, teamID, except string) ([]string, error) {
	return userIDs(ctx, q, `SELECT user_id FROM event_members WHERE event_id = $1 AND team_id = $2 AND user_id::text <> $3`, eventID, teamID, except)
}

// NotifyTeamMembers queues kind for everyone on the team but except.
func NotifyTeamMembers(ctx context.Context, q sqlkit.Querier, eventID, teamID, kind, detail, except string) error {
	ids, err := TeamMemberIDs(ctx, q, eventID, teamID, except)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, eventID, kind, teamID, "", detail, ids...)
}

// NotifyOrganisers queues kind for every organiser of the event.
func NotifyOrganisers(ctx context.Context, q sqlkit.Querier, eventID, kind, refID string) error {
	ids, err := userIDs(ctx, q, `SELECT user_id FROM event_members WHERE event_id = $1 AND role = 'organiser'`, eventID)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, eventID, kind, "", refID, "", ids...)
}

// ListJudgeMemberIDs lists the event's current judge members (role='judge';
// an organiser who also judges is not included, matching the contract's
// "every member with role judge").
func ListJudgeMemberIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	return userIDs(ctx, q, `SELECT user_id FROM event_members WHERE event_id = $1 AND role = 'judge'`, eventID)
}

// NotifyJudgingOpen keeps one undismissed opening notice when judging reopens.
// The stage transaction holds the event lock while checking and creating it.
func NotifyJudgingOpen(ctx context.Context, q sqlkit.Querier, eventID string) error {
	kind := NoticeJudgingOpen
	ids, err := userIDs(ctx, q, `SELECT m.user_id FROM event_members m
		WHERE m.event_id = $1 AND m.role = 'judge'
		AND NOT EXISTS (SELECT 1 FROM event_notices n
			WHERE n.event_id = m.event_id AND n.user_id = m.user_id
			AND n.kind = $2 AND n.seen_at IS NULL)`, eventID, kind)
	if err != nil {
		return err
	}
	return NotifyUsers(ctx, q, eventID, kind, "", "", "", ids...)
}

// NoticeIsJudge reports whether the person is currently a judge member of
// the event (judging_open/judging_reminder's staleness check: "the person is
// no longer a judge").
func NoticeIsJudge(ctx context.Context, q sqlkit.Querier, eventID, userID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2 AND role = 'judge')`,
		eventID, userID).Scan(&ok)
	return ok, err
}

// SupersedeTeamNotices marks earlier notices of these kinds about the team
// seen, and drops their unsent emails: a restored site replaces the
// take-down line, a second deadline change replaces the first, an unlock
// clears "locked". userID narrows it to one person ("" = everyone).
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

// TeamMembersWhoKnew lists the team's members (but except) who could have
// learned of an earlier change of these kinds: everyone except a person whose
// notice of it is still unseen and was never emailed. Its reversal (a site
// put back, an extension taken away) only means something to them. A member
// with no earlier notice at all, from before notices existed, counts as
// knowing.
func TeamMembersWhoKnew(ctx context.Context, q sqlkit.Querier, eventID, teamID, except string, kinds ...string) ([]string, error) {
	return userIDs(ctx, q, `
		SELECT m.user_id FROM event_members m
		 WHERE m.event_id = $1 AND m.team_id = $2 AND m.user_id::text <> $3
		   AND NOT EXISTS (SELECT 1 FROM event_notices n
		         WHERE n.user_id = m.user_id AND n.team_id = $2 AND n.kind = ANY($4)
		           AND n.seen_at IS NULL AND n.email_state IN ('none','pending','skipped'))`,
		eventID, teamID, except, pq.Array(kinds))
}

func userIDs(ctx context.Context, q sqlkit.Querier, query string, args ...any) ([]string, error) {
	rows, err := queryContext(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Notice is one event_notices row, with what a line about it needs.
type Notice = model.Notice

// ListNews is the person's unseen news lines in one event, newest first: at
// most 20, none older than 14 days, team-scoped lines only about their
// current team, nothing once they have left the event.
func ListNews(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]Notice, error) {
	rows, err := queryContext(ctx, q, `
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

// MarkNewsSeen marks the person's own news in one event seen: ids, or every
// line when ids is empty. Returns how many changed. A pending email of a
// line read in the app is still sent: seeing it once is not a reason to drop
// the copy the person asked for.
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

// PendingJoinRequest is one person waiting on a team's admins.
type PendingJoinRequest = model.PendingJoinRequest

// PendingInvite is one team waiting on an invited person.
type PendingInvite = model.PendingInvite

// ListPendingJoinRequests lists the requests waiting on the team's admins,
// oldest first.
func ListPendingJoinRequests(ctx context.Context, q sqlkit.Querier, teamID string) ([]PendingJoinRequest, error) {
	rows, err := queryContext(ctx, q, `
		SELECT r.id, COALESCE(u.display_name,'') FROM event_team_requests r
		  JOIN event_members m ON m.event_id = r.event_id AND m.user_id = r.user_id JOIN users u ON u.id=m.user_id
		 WHERE r.team_id = $1 AND r.kind = 'request' ORDER BY r.created_at, r.id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingJoinRequest{}
	for rows.Next() {
		var p PendingJoinRequest
		if err := rows.Scan(&p.ID, &p.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPendingInvites lists the invites waiting on this person, oldest first.
func ListPendingInvites(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]PendingInvite, error) {
	rows, err := queryContext(ctx, q, `
		SELECT r.id, t.name FROM event_team_requests r JOIN event_teams t ON t.id = r.team_id
		 WHERE r.event_id = $1 AND r.user_id = $2 AND r.kind = 'invite' ORDER BY r.created_at, r.id`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingInvite{}
	for rows.Next() {
		var p PendingInvite
		if err := rows.Scan(&p.ID, &p.TeamName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountWaitingApplications counts every participant waiting for an
// organiser's decision, by application or by a join link that needs approval.
func CountWaitingApplications(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_members WHERE event_id = $1 AND role = 'participant' AND approval_status = 'pending'`, eventID).Scan(&n)
	return n, err
}

// EmailUpdates reads the person's "Email me when something waits on me"
// setting (on by default) and whether their account has an address at all.
func EmailUpdates(ctx context.Context, q sqlkit.Querier, userID string) (on, possible bool, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(p.email_updates, true), u.username LIKE '%@%' AND NOT u.event_account
		  FROM users u LEFT JOIN hack_account_preferences p ON p.user_id = u.id WHERE u.id = $1`, userID).Scan(&on, &possible)
	return on, possible, err
}

// MailBatch is one person's pending notices of one kind, locked until the
// caller commits.
type MailBatch = model.MailBatch

// MailNotice is a pending notice with its event, for the email text.
type MailNotice = model.MailNotice

// NextMailBatch picks the oldest person and kind with a notice that waited
// at least delay and no email of that kind in the last window, takes an
// advisory lock on that person and kind (so two sweepers can never both send
// inside one window), re-checks the window under it, and locks every pending
// notice of theirs of that kind. sql.ErrNoRows when nothing is due.
func NextMailBatch(ctx context.Context, tx *sql.Tx, delay, window time.Duration) (MailBatch, error) {
	var b MailBatch
	secs := func(d time.Duration) float64 { return d.Seconds() }
	err := tx.QueryRowContext(ctx, `
		SELECT n.user_id, n.kind FROM event_notices n
		 WHERE n.email_state = 'pending' AND EXISTS (SELECT 1 FROM events e WHERE e.id = n.event_id) AND n.created_at <= now() - make_interval(secs => $1)
		   AND (n.attempted_at IS NULL OR n.attempted_at <= now() - interval '5 minutes')
		   AND NOT EXISTS (SELECT 1 FROM event_notices s WHERE s.user_id = n.user_id AND s.kind = n.kind
		         AND s.email_state = 'sent' AND s.emailed_at > now() - make_interval(secs => $2))
		 ORDER BY n.created_at, n.id LIMIT 1 FOR UPDATE SKIP LOCKED`, secs(delay), secs(window)).Scan(&b.UserID, &b.Kind)
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
		// Another sweeper sent this kind while we waited for the lock. Leave
		// the rows pending for the next window.
		return b, errMailBatchRaced
	}
	var recipient string
	var ok bool
	err = tx.QueryRowContext(ctx, `SELECT u.username, `+emailableSQL+` FROM users u
		LEFT JOIN hack_account_preferences p ON p.user_id = u.id WHERE u.id = $1`, b.UserID).Scan(&recipient, &ok)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if ok {
		b.Recipient = recipient
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.event_id, n.user_id, n.kind, COALESCE(n.team_id::text,''), COALESCE(n.ref_id::text,''), n.detail, n.created_at,
		       e.slug, e.title, e.time_zone
		  FROM event_notices n JOIN events e ON e.id = n.event_id
		 WHERE n.user_id = $1 AND n.kind = $2 AND n.email_state = 'pending'
		   AND n.created_at <= now() - make_interval(secs => $3)
		 ORDER BY e.title, e.id, n.created_at, n.id LIMIT 200 FOR UPDATE OF n SKIP LOCKED`, b.UserID, b.Kind, secs(delay))
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var m MailNotice
		if err := rows.Scan(&m.ID, &m.EventID, &m.UserID, &m.Kind, &m.TeamID, &m.RefID, &m.Detail, &m.CreatedAt,
			&m.EventSlug, &m.EventTitle, &m.EventTimeZone); err != nil {
			return b, err
		}
		b.Notices = append(b.Notices, m)
	}
	return b, rows.Err()
}

var errMailBatchRaced = errors.New("notice mail: another sweeper sent this kind")

// IsMailBatchRaced reports NextMailBatch's lost race (nothing to do this time).
func IsMailBatchRaced(err error) bool { return errors.Is(err, errMailBatchRaced) }

// FinishMailNotices sets the email state of these notices: sent (with
// emailed_at, which starts the person's window for this kind) or skipped.
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

// PruneNotices deletes notices older than 30 days, seen or not, and any
// whose event is gone (event_notices has no foreign key to events).
func PruneNotices(ctx context.Context, q sqlkit.Querier) error {
	_, err := PruneNoticesCount(ctx, q)
	return err
}

func PruneNoticesCount(ctx context.Context, q sqlkit.Querier) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM event_notices n WHERE n.created_at < now() - interval '30 days'
		OR NOT EXISTS (SELECT 1 FROM events e WHERE e.id = n.event_id)`)
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
