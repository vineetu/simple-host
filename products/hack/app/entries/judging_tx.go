package entries

import (
	"context"
	"database/sql"
	"errors"
	"log"

	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

func (s *Service) DeleteFormerJudgeScores(ctx context.Context, eventID, callerID, userID, slug string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if ev.TakenDown() {
		return &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	if ev.Stage == "archived" {
		return errEventClosed
	}
	if ev.JudgingLockedAt.Valid {
		return errScoresLocked
	}
	// Re-check membership under the event lock: a concurrent organiser removal
	// may have committed while this request waited for that lock.
	caller, err := hackmembers.Member(ctx, tx, eventID, callerID, hackmembers.ForShare)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !caller.Organiser() {
		return &Error{Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return err
	}
	found, err := entryrepo.DeleteFormerJudgeScores(ctx, tx, eventID, userID)
	if err != nil {
		log.Printf("hack: delete former judge scores %s: %v", slug, err)
		return err
	}
	if !found {
		return &Error{Code: "former_judge_not_found", Message: "former judge not found"}
	}
	return tx.Commit()
}
