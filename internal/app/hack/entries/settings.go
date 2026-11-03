package entries

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
)

// JudgingSettings contains the optional settings to change.
type JudgingSettings struct {
	Mode                      *string
	PerTeam                   *int
	ScoreMode, TieCriterionID *string
	PublicScores, PublicRanks *bool
}

// UpdateJudgingSettings applies settings under the event lock.
func (s *Service) UpdateJudgingSettings(ctx context.Context, eventID string, req JudgingSettings) (db.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, err
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return db.Event{}, err
	}
	if ev.Stage == "archived" {
		return db.Event{}, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if (req.Mode != nil || req.PerTeam != nil) && ev.JudgingLockedAt.Valid {
		return db.Event{}, &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first."}
	}
	if req.TieCriterionID != nil {
		if ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived" {
			return db.Event{}, &Error{Code: "tie_rule_closed", Message: "choose the tie criterion before judging begins"}
		}
		if *req.TieCriterionID != "" {
			valid, err := entryrepo.EventCriterionInEvent(ctx, tx, ev.ID, *req.TieCriterionID)
			if err != nil {
				return db.Event{}, err
			}
			if !valid {
				return db.Event{}, &Error{Code: "criterion_not_found", Message: "criterion not in this event"}
			}
		}
	}
	if req.Mode != nil || req.PerTeam != nil {
		// TODO(4.1): Move this event update with its event scanner in step 4.2.
		ev, err = db.UpdateJudgingSettings(ctx, tx, ev.ID, req.Mode, req.PerTeam)
		if err != nil {
			return db.Event{}, err
		}
	}
	var scoreMode, publicScores, publicRanks any
	if req.ScoreMode != nil {
		scoreMode = *req.ScoreMode
	}
	if req.PublicScores != nil {
		publicScores = *req.PublicScores
	}
	if req.PublicRanks != nil {
		publicRanks = *req.PublicRanks
	}
	if req.ScoreMode != nil || req.TieCriterionID != nil || req.PublicScores != nil || req.PublicRanks != nil {
		var tie any
		if req.TieCriterionID != nil {
			tie = *req.TieCriterionID
		}
		// TODO(4.2): Move this existing event update into the event repository in step 4.2.
		_, err = tx.ExecContext(ctx, `UPDATE events SET score_mode=COALESCE($2::text,score_mode),
			tie_criterion_id=CASE WHEN $3::boolean THEN NULLIF($4::text,'')::uuid ELSE tie_criterion_id END,
			public_scores=COALESCE($5::boolean,public_scores), public_ranks=COALESCE($6::boolean,public_ranks), updated_at=now() WHERE id=$1`, ev.ID, scoreMode, req.TieCriterionID != nil, tie, publicScores, publicRanks)
		if err != nil {
			return db.Event{}, err
		}
	}
	ev, err = db.GetEventForUpdate(ctx, tx, ev.ID)
	if err != nil {
		return db.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return db.Event{}, err
	}
	return ev, nil
}

// SetJudgingLock sets or clears the event's judging lock.
func (s *Service) SetJudgingLock(ctx context.Context, eventID string, locked bool, reason string) (db.Event, error) {
	// TODO(4.1): Move this event update with its event scanner in step 4.2.
	return db.SetJudgingLock(ctx, s.db, eventID, locked, reason)
}
