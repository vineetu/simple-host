package entries

import (
	"context"
	"fmt"

	model "github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

// ScoreInput is a criterion and the submitted points.
type ScoreInput struct {
	CriterionID string   `json:"criterion_id"`
	Points      *float64 `json:"points"`
}

// JudgeComment validates a comment before the scoring transaction begins.
func (s *Service) JudgeComment(text string) (string, error) {
	cleaned, msg := hackentries.JudgeCommentText(text)
	if msg != "" {
		return "", &Error{Code: "invalid_comment", Message: msg}
	}
	return cleaned, nil
}

// JudgeScores reads a judge's scores after checking eligibility.
func (s *Service) JudgeScores(ctx context.Context, eventID, judgeID, teamID string) ([]entryrepo.JudgeScore, string, error) {
	if _, err := entryrepo.EventTeamInEvent(ctx, s.db, eventID, teamID); err != nil {
		return nil, "", err
	}
	ok, err := entryrepo.JudgeTeamEligible(ctx, s.db, eventID, judgeID, teamID)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", &Error{Code: "forbidden", Message: "You have a conflict with this team."}
	}
	return entryrepo.GetJudgeTeamScores(ctx, s.db, eventID, judgeID, teamID)
}

// PutJudgeScores validates and saves scores while holding the event lock.
func (s *Service) PutJudgeScores(ctx context.Context, eventID, judgeID, teamID string, inputs []ScoreInput, comment *string, scoringStage func(context.Context, model.Event) bool) ([]entryrepo.JudgeScore, string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, "", false, err
	}
	if ev.JudgingLockedAt.Valid {
		return nil, "", false, &Error{Code: "scores_locked", Message: "Judging is locked because the event is at Results. Move the event back to Judging to change it."}
	}
	if !scoringStage(ctx, ev) {
		return nil, "", false, &Error{Code: "scoring_not_open", Message: "Scoring opens when judging starts."}
	}
	member, err := entryrepo.EventJudgeInEvent(ctx, tx, eventID, judgeID)
	if err != nil {
		return nil, "", false, err
	}
	if !member {
		message := "You are no longer a judge or organiser of this event."
		ids, err := eventrepo.OrganiserIDs(ctx, tx, eventID)
		if err != nil {
			return nil, "", false, err
		}
		for _, id := range ids {
			if id == judgeID {
				message = "You are not a judge of this event."
				break
			}
		}
		return nil, "", false, &Error{Code: "forbidden", Message: message}
	}
	crit, err := entryrepo.CriterionIDsForEvent(ctx, tx, eventID)
	if err != nil {
		return nil, "", false, err
	}
	scores := make([]entryrepo.JudgeScore, 0, len(inputs))
	for _, v := range inputs {
		max, ok := crit[v.CriterionID]
		if !ok {
			return nil, "", false, &Error{Code: "criterion_not_found", Message: "That criterion is not in this event's current rubric."}
		}
		pts, ok := hackentries.WholeInRange(v.Points, 0, max)
		if !ok {
			return nil, "", false, &Error{Code: "invalid_points", Message: fmt.Sprintf("Points for %q must be a whole number from 0 to %d.", v.CriterionID, max)}
		}
		scores = append(scores, entryrepo.JudgeScore{CriterionID: v.CriterionID, Points: pts})
	}
	if comment != nil && len(scores) == 0 {
		existing, _, err := entryrepo.GetJudgeTeamScores(ctx, tx, eventID, judgeID, teamID)
		if err != nil {
			return nil, "", false, err
		}
		if len(existing) == 0 {
			return nil, "", false, &Error{Code: "invalid_request", Message: "Score at least one criterion before adding a comment."}
		}
	}
	eligible, err := entryrepo.JudgeTeamEligible(ctx, tx, eventID, judgeID, teamID)
	if err != nil {
		return nil, "", false, err
	}
	if !eligible {
		return nil, "", false, &Error{Code: "forbidden", Message: "This team is not in your judging queue."}
	}
	updated, text, err := entryrepo.UpsertJudgeScores(ctx, tx, eventID, judgeID, teamID, scores, comment)
	if err != nil {
		return nil, "", false, err
	}
	rubric, err := entryrepo.ListRubric(ctx, tx, eventID)
	if err != nil {
		return nil, "", false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", false, err
	}
	return updated, text, len(rubric) > 0 && len(updated) >= len(rubric), nil
}

// TeamComments returns the comments for a team's results.
func (s *Service) TeamComments(ctx context.Context, eventID, teamID string) ([]string, error) {
	return entryrepo.TeamComments(ctx, s.db, eventID, teamID)
}
