package entries

import (
	"context"
	"database/sql"
	"errors"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"log"
)

// DeleteFormerJudgeScores removes a departed judge's scores under the event and membership locks.
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
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if ev.JudgingLockedAt.Valid {
		return &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first."}
	}
	// Re-check membership under the event lock: a concurrent organiser removal
	// may have committed while this request waited for that lock.
	role, err := entryrepo.OrganiserRoleForShare(ctx, tx, eventID, callerID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && role != "organiser" {
		return &Error{Code: "event_not_found"}
	}
	if err != nil {
		return err
	}
	found, err := eventrepo.DeleteFormerJudgeScores(ctx, tx, eventID, userID)
	if err != nil {
		log.Printf("hack: delete former judge scores %s: %v", slug, err)
		return err
	}
	if !found {
		return &Error{Code: "former_judge_not_found", Message: "former judge not found"}
	}
	return tx.Commit()
}
