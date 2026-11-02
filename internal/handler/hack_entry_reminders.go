package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Entry deadline reminders (entries slice 3, zx-hack-entry-reminders.sql).
// 24 hours and 1 hour before a team's effective deadline, every current
// member of a team whose entry is not submitted gets one email. They ride
// the notices queue (kind entry_reminder), so the Account switch "Email me
// when something waits on me", the no-email rule for key-only accounts and
// the per-kind batching all apply as they do to every other notice. The
// text is written at send time from the entry as it is then, with the
// missing lines named by the one rules engine (evaluateEntry).

// queueEntryReminders runs on the content-mail ticker, before the notice
// sweep. Each due team is claimed once per reminder per deadline, then its
// members are queued in the same transaction.
func (h *HackHandler) queueEntryReminders(ctx context.Context) {
	due, err := db.DueEntryReminders(ctx, h.database)
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return
	}
	for _, r := range due {
		if err := h.queueEntryReminder(ctx, r); err != nil {
			log.Printf("hack: entry reminder %s: %v", r.TeamID, err)
		}
	}
}

func (h *HackHandler) queueEntryReminder(ctx context.Context, r db.EntryReminder) error {
	tx, err := h.database.BeginTx(ctx, nil)
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

// entryReminderLine re-reads the team and its entry and writes the
// reminder's line. ok=false drops it: the entry was submitted meanwhile, the
// deadline passed, or the team can no longer change its entry.
func (h *HackHandler) entryReminderLine(ctx context.Context, tx *sql.Tx, n db.MailNotice) (string, bool, error) {
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
		" (" + reminderIn(st.Deadline.Time.Sub(st.Now)) + ")."
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
	check, err := h.evaluateEntryTx(ctx, tx, ev, st, n.TeamID, e)
	if err != nil {
		return "", false, err
	}
	if check.Complete {
		return "Your team's entry is complete but not submitted yet. " + closes +
			" Submit it now, or it is submitted for you at the deadline.", true, nil
	}
	return "Your team's entry isn't submitted yet. " + closes + " Still missing: " +
		oneLine(entryMissingMessage(check.Missing, loadEntryRules(ev.EntryRules))) +
		". If it isn't complete by then, it is still judged, marked incomplete.", true, nil
}

// reminderIn is the plain "in about 3 hours" for the time left.
func reminderIn(d time.Duration) string {
	switch {
	case d < 45*time.Minute:
		return "in less than an hour"
	case d < 90*time.Minute:
		return "in about an hour"
	}
	return fmt.Sprintf("in about %d hours", int(math.Round(d.Hours())))
}
