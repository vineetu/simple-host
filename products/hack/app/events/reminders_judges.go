package events

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

func (s *Service) QueueJudgeReminders(ctx context.Context, judges EntryEvaluator) (written int64, firstErr error) {
	eventIDs, err := eventrepo.JudgeReminderEvents(ctx, s.db)
	var judgesDue []model.EventMember
	if err == nil && len(eventIDs) > 0 {
		judgesDue, err = hackmembers.Members(ctx, s.db, hackmembers.Filter{EventIDs: eventIDs, Role: "judge"})
	}
	var due []model.JudgeReminderDue
	if err == nil {
		due, err = eventrepo.DueJudgeReminders(ctx, s.db, judgesDue)
	}
	if err == nil {
		due, err = entryrepo.UnsentJudgeReminders(ctx, s.db, due)
	}
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

// queueOneJudgeReminder re-reads judging_ends_at under the event row's FOR SHARE lock before claiming.
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
	claimed, err := entryrepo.ClaimJudgeReminder(ctx, tx, d.EventID, d.UserID, d.Kind, d.Deadline)
	if err != nil || !claimed {
		return 0, err
	}
	var n int64
	if notify {
		n, err = hackmembers.NotifyUsersCount(ctx, tx, d.EventID, hackmembers.NoticeJudgingReminder, "", "", reminderDetail(d.Kind, d.Deadline), d.UserID)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The durable reminder claim is also a write when the member is not emailable.
	return n + 1, nil
}
