package events

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Judging-close reminders (0003-hack-notices-judges-messages.sql). Rides the
// same content-mail ticker as QueueEntryReminders, before the notice sweep.
// Only judges with at least one unscored team in their queue are reminded;
// N comes from the entries service through EntryEvaluator.UnscoredTeams,
// wired from hackhttp.

// QueueJudgeReminders claims each due (event, judge) pair once per deadline,
// then queues (with a notice) the judges whose queue still has unscored
// teams. A judge with nothing left to score at this offset is still
// claimed, just without a notice (queueOneJudgeReminder's notify=false): the
// claim row is what DueJudgeReminders' NOT EXISTS filters on, so without it
// that judge would be re-selected by every sweep forever, and can crowd out
// judges still due once a busy event has more than LIMIT 500 of them stuck
// this way. Events are cached by id since DueJudgeReminders can return
// several judges per event.
func (s *Service) QueueJudgeReminders(ctx context.Context, judges EntryEvaluator) (written int64, firstErr error) {
	due, err := eventrepo.DueJudgeReminders(ctx, s.db)
	if err != nil {
		log.Printf("hack: judge reminders: %v", err)
		return 0, err
	}
	events := map[string]model.Event{}
	for _, d := range due {
		ev, ok := events[d.EventID]
		if !ok {
			ev, err = eventrepo.GetEventByID(ctx, s.db, d.EventID)
			if err != nil {
				log.Printf("hack: judge reminder %s: %v", d.EventID, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			events[d.EventID] = ev
		}
		count, err := judges.UnscoredTeams(ctx, ev, d.UserID)
		if err != nil {
			log.Printf("hack: judge reminder %s: %v", d.EventID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n, err := s.queueOneJudgeReminder(ctx, d, count > 0)
		written += n
		if err != nil {
			log.Printf("hack: judge reminder %s: %v", d.EventID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return
}

// queueOneJudgeReminder re-reads judging_ends_at under the event row's FOR
// SHARE lock before claiming: a sweep that picked up d as a candidate, then
// stalled, must not claim (and overwrite) a deadline that has since moved
// -- ClaimJudgeReminder's own ON CONFLICT would otherwise treat this stale
// deadline as a legitimate re-arm and undo a newer claim already recorded
// for the real current deadline.
//
// notify=false (the judge's queue had nothing unscored as of QueueJudge
// Reminders' own check) still claims the row, just skips the notice: the
// claim, not the notice, is what keeps this judge out of every later
// sweep's DueJudgeReminders batch for this exact deadline. Without it, a
// judge who is simply done for this offset would never be claimable and
// would reappear in the LIMIT 500 candidate batch forever, potentially
// crowding out judges still actually due (the starvation this guards
// against).
func (s *Service) queueOneJudgeReminder(ctx context.Context, d model.JudgeReminderDue, notify bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForShare(ctx, tx, d.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !ev.JudgingEndsAt.Valid || !ev.JudgingEndsAt.Time.Equal(d.Deadline) {
		return 0, nil
	}
	claimed, err := eventrepo.ClaimJudgeReminder(ctx, tx, d.EventID, d.UserID, d.Kind, d.Deadline)
	if err != nil || !claimed {
		return 0, err
	}
	var n int64
	if notify {
		n, err = eventrepo.NotifyUsersCount(ctx, tx, d.EventID, eventrepo.NoticeJudgingReminder, "", "", reminderDetail(d.Kind, d.Deadline), d.UserID)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The durable reminder claim is also a write when the member is not
	// emailable (matches queueEntryReminder's own accounting), or when
	// there is no notice to send at all (notify=false).
	return n + 1, nil
}
