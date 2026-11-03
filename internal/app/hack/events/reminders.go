package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"log"
	"math"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// QueueEntryReminders claims due reminders and queues notices for each team.
func (s *Service) QueueEntryReminders(ctx context.Context) {
	due, err := db.DueEntryReminders(ctx, s.db)
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

func (s *Service) queueEntryReminder(ctx context.Context, r db.EntryReminder) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	claimed, err := db.ClaimEntryReminder(ctx, tx, r)
	if err != nil || !claimed {
		return err
	}
	if err := db.NotifyTeamMembers(ctx, tx, r.EventID, r.TeamID, db.NoticeEntryReminder, r.Kind, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// EntryEvaluator evaluates an entry using the existing entry service.
type EntryEvaluator interface {
	EvaluateEntryTx(context.Context, *sql.Tx, db.Event, db.TeamWriteState, string, model.EventEntry) (hackentries.Check, error)
}

// entryReminderLine re-reads the team and its entry and writes the
// reminder's line. ok=false drops it: the entry was submitted meanwhile, the
// deadline passed, or the team can no longer change its entry.
func (s *Service) entryReminderLine(ctx context.Context, tx *sql.Tx, n db.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	st, err := db.TeamWriteStateFor(ctx, tx, n.TeamID, false)
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
	e, err := db.GetEventEntry(ctx, tx, n.TeamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "Your team hasn't started its entry. " + closes + " A team with no entry isn't judged.", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if e.SubmittedAt.Valid {
		return "", false, nil
	}
	ev, err := db.GetEventByID(ctx, tx, st.EventID)
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
