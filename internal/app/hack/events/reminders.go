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
func (s *Service) QueueEntryReminders(ctx context.Context) {
	due, err := eventrepo.DueEntryReminders(ctx, s.db)
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return
	}
	for _, r := range due {
		if err := s.queueEntryReminder(ctx, r); err != nil {
			log.Printf("hack: entry reminder %s: %v", r.TeamID, err)
		}
	}
}

func (s *Service) queueEntryReminder(ctx context.Context, r model.EntryReminder) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	claimed, err := eventrepo.ClaimEntryReminder(ctx, tx, r)
	if err != nil || !claimed {
		return err
	}
	if err := eventrepo.NotifyTeamMembers(ctx, tx, r.EventID, r.TeamID, eventrepo.NoticeEntryReminder, r.Kind, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// EntryEvaluator evaluates an entry using the existing entry service.
type EntryEvaluator interface {
	EvaluateEntryTx(context.Context, *sql.Tx, model.Event, model.TeamWriteState, string, model.EventEntry) (hackentries.Check, error)
}

// entryReminderLine re-reads the team and its entry and writes the
// reminder's line. ok=false drops it: the entry was submitted meanwhile, the
// deadline passed, or the team can no longer change its entry.
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
	ev, err := eventrepo.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return "", false, err
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
