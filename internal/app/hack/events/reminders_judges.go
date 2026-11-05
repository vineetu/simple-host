package events

import (
	"log"

	"context"

	"github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
)

// Judging-close reminders (0003-hack-notices-judges-messages.sql). Rides the
// same content-mail ticker as QueueEntryReminders, before the notice sweep.
// Only judges with at least one unscored team in their queue are reminded;
// N comes from the entries service through EntryEvaluator.UnscoredTeams,
// wired from hackhttp.

// QueueJudgeReminders claims each due event's offset once per judge per
// deadline, then queues the judges whose queue still has unscored teams.
func (s *Service) QueueJudgeReminders(ctx context.Context, judges EntryEvaluator) (written int64, firstErr error) {
	due, err := eventrepo.DueJudgeReminders(ctx, s.db)
	if err != nil {
		log.Printf("hack: judge reminders: %v", err)
		return 0, err
	}
	for _, d := range due {
		n, err := s.queueJudgeReminder(ctx, d, judges)
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

func (s *Service) queueJudgeReminder(ctx context.Context, d model.JudgeReminderDue, judges EntryEvaluator) (int64, error) {
	ev, err := eventrepo.GetEventByID(ctx, s.db, d.EventID)
	if err != nil {
		return 0, err
	}
	judgeIDs, err := eventrepo.ListJudgeMemberIDs(ctx, s.db, d.EventID)
	if err != nil {
		return 0, err
	}
	var written int64
	for _, judgeID := range judgeIDs {
		count, err := judges.UnscoredTeams(ctx, ev, judgeID)
		if err != nil {
			return written, err
		}
		if count == 0 {
			continue
		}
		n, err := s.queueOneJudgeReminder(ctx, d, judgeID)
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

func (s *Service) queueOneJudgeReminder(ctx context.Context, d model.JudgeReminderDue, judgeID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	claimed, err := eventrepo.ClaimJudgeReminder(ctx, tx, d.EventID, judgeID, d.Kind, d.Deadline)
	if err != nil || !claimed {
		return 0, err
	}
	n, err := eventrepo.NotifyUsersCount(ctx, tx, d.EventID, eventrepo.NoticeJudgingReminder, "", "", d.Kind, judgeID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The durable reminder claim is also a write when the member is not
	// emailable (matches queueEntryReminder's own accounting).
	return n + 1, nil
}
