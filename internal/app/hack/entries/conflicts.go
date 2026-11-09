package entries

import (
	"context"
	"database/sql"
	"errors"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
)

// Conflicts lists the event's conflicts, optionally restricted to a judge.
func (s *Service) Conflicts(ctx context.Context, eventID, judgeID string) ([]entryrepo.EventConflict, error) {
	return entryrepo.ListEventConflicts(ctx, s.db, eventID, judgeID)
}

// DeclareConflict records a conflict under the event lock.
func (s *Service) DeclareConflict(ctx context.Context, eventID, judgeID, teamID, declaredBy string) (entryrepo.EventConflict, error) {
	var empty entryrepo.EventConflict
	tx, ev, err := s.optionsTx(ctx, eventID, false)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	_ = ev
	if _, err := entryrepo.EventTeamInEvent(ctx, tx, eventID, teamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, &Error{Code: "team_not_found", Message: "team not found"}
		}
		return empty, err
	}
	judge, err := entryrepo.EventJudge(ctx, tx, eventID, judgeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, &Error{Code: "judge_not_found", Message: "judge not found"}
		}
		return empty, err
	}
	if err = entryrepo.DeclareConflict(ctx, tx, eventID, judge.ID, teamID, declaredBy); err != nil {
		return empty, err
	}
	saved, err := entryrepo.GetEventConflict(ctx, tx, eventID, judge.ID, teamID)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return saved, nil
}

// DeleteConflict removes a conflict under the event lock.
func (s *Service) DeleteConflict(ctx context.Context, eventID, judgeID, teamID string) error {
	tx, _, err := s.optionsTx(ctx, eventID, false)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	gone, err := entryrepo.DeleteConflict(ctx, tx, eventID, judgeID, teamID)
	if err != nil {
		return err
	}
	if !gone {
		return &Error{Code: "conflict_not_found", Message: "conflict not found"}
	}
	return tx.Commit()
}

// CheckScoreConflict checks the team and conflicts before score request decoding.
func (s *Service) CheckScoreConflict(ctx context.Context, eventID, judgeID, teamID string) error {
	_, err := entryrepo.EventTeamInEvent(ctx, s.db, eventID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "team_not_found", Message: "team not found"}
	}
	if err != nil {
		return err
	}
	pairs, err := entryrepo.ListConflictPairs(ctx, s.db, eventID)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if p.JudgeID == judgeID && p.TeamID == teamID {
			return &Error{Code: "forbidden", Message: "You have a conflict with this team."}
		}
	}
	return nil
}
