package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// Entry deadline reminders (entries slice 3, zx-hack-entry-reminders.sql).
// 24 hours and 1 hour before a team's effective deadline, every current
// member of a team whose entry is not submitted gets one email. They ride
// the notices queue (kind entry_reminder), so the Account switch "Email me
// when something waits on me", the no-email rule for key-only accounts and
// the per-kind batching all apply as they do to every other notice. The
// text is written at send time from the entry as it is then, with the
// missing lines named by the one rules engine (hackentries.Evaluate).

// QueueEntryReminders claims each due team once per reminder per deadline,
// then queues its members in the same transaction.
func (s *Service) QueueEntryReminders(ctx context.Context) (written int64, firstErr error) {
	due, err := eventrepo.DueEntryReminders(ctx, s.db)
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return 0, err
	}
	for _, r := range due {
		n, err := s.queueEntryReminder(ctx, r)
		written += n
		if err != nil {
			log.Printf("hack: entry reminder %s: %v", r.TeamID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return
}

// queueEntryReminder re-reads the team's effective deadline under its own
// FOR SHARE lock before claiming: a sweep that picked up r as a candidate,
// then stalled, must not claim (and overwrite) a deadline that has since
// moved -- ClaimEntryReminder's own ON CONFLICT would otherwise treat this
// stale deadline as a legitimate re-arm and undo a newer claim already
// recorded for the real current deadline (same race as judge reminders,
// queueOneJudgeReminder).
func (s *Service) queueEntryReminder(ctx context.Context, r model.EntryReminder) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, r.TeamID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !st.Deadline.Valid || !st.Deadline.Time.Equal(r.Deadline) {
		return 0, nil
	}
	claimed, err := eventrepo.ClaimEntryReminder(ctx, tx, r)
	if err != nil || !claimed {
		return 0, err
	}
	ids, err := eventrepo.TeamMemberIDs(ctx, tx, r.EventID, r.TeamID, "")
	if err != nil {
		return 0, err
	}
	n, err := eventrepo.NotifyUsersCount(ctx, tx, r.EventID, eventrepo.NoticeEntryReminder, r.TeamID, "", r.Kind, ids...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The durable reminder claim is also a write when there are no emailable members.
	return n + 1, nil
}

// EntryEvaluator is the events service's small interface over the entries
// service (0003-hack-notices-judges-messages.sql: "like EntryEvaluator"),
// wired from hackhttp so this package never imports app/hack/entries
// directly. EvaluateEntryTx checks an entry's completeness for the entry
// reminder's text; QueueCounts gives a judge's queue size and how many of
// those rows are not yet Done, for judging_open/judging_reminder's text,
// both live (NeedsYou) and at send time (the mail sweep); UnscoredTeams is
// the same unscored count alone, for callers that only need to know
// whether to remind a judge at all.
type EntryEvaluator interface {
	EvaluateEntryTx(context.Context, *sql.Tx, model.Event, model.TeamWriteState, string, model.EventEntry) (hackentries.Check, error)
	UnscoredTeams(ctx context.Context, ev model.Event, judgeID string) (int, error)
	QueueCounts(ctx context.Context, ev model.Event, judgeID string) (total, unscored int, err error)
}

// reminderOffsetStale reports whether a queued reminder -- read back at
// send time with its offset label (n.Detail) and the time now remaining to
// its deadline -- no longer describes a reminder the event would still
// send today (0003-hack-notices-judges-messages.sql: dropped the same way
// as any other stale notice). True when the organiser removed that offset
// from reminder_offsets since the reminder was queued, or when the
// deadline moved later: the remaining time now exceeds the offset by more
// than 30 minutes (comfortably past the sweep's own polling interval), so
// what's left is no longer "about to happen", it's a fresh wait for the
// next due pass at the new deadline.
func reminderOffsetStale(offsets []int64, label string, remaining time.Duration) bool {
	for _, o := range offsets {
		if ReminderLabel(int(o)) == label {
			return remaining > time.Duration(o)*time.Minute+30*time.Minute
		}
	}
	return true
}

// entryReminderLine re-reads the team and its entry and writes the
// reminder's line. ok=false drops it: the entry was submitted meanwhile, the
// deadline passed, the team can no longer change its entry, or the queued
// offset no longer matches the event's current reminder_offsets/deadline
// (reminderOffsetStale).
func (s *Service) entryReminderLine(ctx context.Context, tx *sql.Tx, n model.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, n.TeamID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if st.WriteRefusal() != "" || !st.Deadline.Valid || !st.Now.Before(st.Deadline.Time) {
		return "", false, nil
	}
	ev, err := eventrepo.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return "", false, err
	}
	if reminderOffsetStale(ev.ReminderOffsets, n.Detail, st.Deadline.Time.Sub(st.Now)) {
		return "", false, nil
	}
	closes := "Entries close for your team " + hackNoticeTime(st.Deadline.Time, n.EventTimeZone) +
		" (" + ReminderIn(st.Deadline.Time.Sub(st.Now)) + ")."
	e, err := entryrepo.GetEventEntry(ctx, tx, n.TeamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "Your team hasn't started its entry. " + closes + " A team with no entry isn't judged.", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if e.SubmittedAt.Valid {
		return "", false, nil
	}
	check, err := evaluator.EvaluateEntryTx(ctx, tx, ev, st, n.TeamID, e)
	if err != nil {
		return "", false, err
	}
	if check.Complete {
		return "Your team's entry is complete but not submitted yet. " + closes +
			" Submit it now, or it is submitted for you at the deadline.", true, nil
	}
	return "Your team's entry isn't submitted yet. " + closes + " Still missing: " +
		oneLine(hackentries.MissingMessage(check.Missing, hackentries.LoadRules(ev.EntryRules))) +
		". If it isn't complete by then, it is still judged, marked incomplete.", true, nil
}

// ReminderIn is the plain "in about 3 hours" for the time left.
func ReminderIn(d time.Duration) string {
	switch {
	case d < 45*time.Minute:
		return "in less than an hour"
	case d < 90*time.Minute:
		return "in about an hour"
	}
	return fmt.Sprintf("in about %d hours", int(math.Round(d.Hours())))
}
