package entries

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	model "github.com/vsriram/simple-host/products/hack/model"
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

// PutJudgeScores saves scores under the event lock; done means every criterion is scored.
func (s *Service) PutJudgeScores(ctx context.Context, eventID, judgeID, teamID string, inputs []ScoreInput, comment *string, scoringStage func(context.Context, model.Event) bool) (scores []entryrepo.JudgeScore, text string, done bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", false, err
	}
	defer tx.Rollback()
	if scores, text, err = saveJudgeScores(ctx, tx, eventID, judgeID, teamID, inputs, comment, scoringStage); err != nil {
		return nil, "", false, err
	}
	rubric, err := entryrepo.ListRubric(ctx, tx, eventID)
	if err != nil {
		return nil, "", false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", false, err
	}
	return scores, text, len(rubric) > 0 && len(scores) >= len(rubric), nil
}

// saveJudgeScores locks the event, refuses a judge who may not score teamID now, and upserts.
func saveJudgeScores(ctx context.Context, tx *sql.Tx, eventID, judgeID, teamID string, inputs []ScoreInput, comment *string, scoringStage func(context.Context, model.Event) bool) ([]entryrepo.JudgeScore, string, error) {
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, "", err
	}
	if err := JudgingLocked(ev); err != nil {
		return nil, "", err
	}
	if !scoringStage(ctx, ev) {
		return nil, "", &Error{Code: "scoring_not_open", Message: "Scoring opens when judging starts."}
	}
	if err := checkScoringJudge(ctx, tx, eventID, judgeID); err != nil {
		return nil, "", err
	}
	crit, err := entryrepo.CriterionIDsForEvent(ctx, tx, eventID)
	if err != nil {
		return nil, "", err
	}
	scores, err := parseScores(crit, inputs)
	if err != nil {
		return nil, "", err
	}
	if comment != nil && len(scores) == 0 {
		existing, _, err := entryrepo.GetJudgeTeamScores(ctx, tx, eventID, judgeID, teamID)
		if err != nil {
			return nil, "", err
		}
		if len(existing) == 0 {
			return nil, "", &Error{Code: "invalid_request", Message: "Score at least one criterion before adding a comment."}
		}
	}
	eligible, err := entryrepo.JudgeTeamEligible(ctx, tx, eventID, judgeID, teamID)
	if err != nil {
		return nil, "", err
	}
	if !eligible {
		return nil, "", &Error{Code: "forbidden", Message: "This team is not in your judging queue."}
	}
	return entryrepo.UpsertJudgeScores(ctx, tx, eventID, judgeID, teamID, scores, comment)
}

// checkScoringJudge refuses a non-judge, telling an organiser apart from someone who left.
func checkScoringJudge(ctx context.Context, tx *sql.Tx, eventID, judgeID string) error {
	member, err := entryrepo.EventJudgeInEvent(ctx, tx, eventID, judgeID)
	if err != nil || member {
		return err
	}
	ids, err := eventrepo.OrganiserIDs(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if slices.Contains(ids, judgeID) {
		return &Error{Code: "forbidden", Message: "You are not a judge of this event."}
	}
	return &Error{Code: "forbidden", Message: "You are no longer a judge or organiser of this event."}
}

// parseScores checks points against crit (criterion id to maximum points).
func parseScores(crit map[string]int, inputs []ScoreInput) ([]entryrepo.JudgeScore, error) {
	scores := make([]entryrepo.JudgeScore, 0, len(inputs))
	for _, v := range inputs {
		max, ok := crit[v.CriterionID]
		if !ok {
			return nil, &Error{Code: "criterion_not_found", Message: "That criterion is not in this event's current rubric."}
		}
		pts, ok := hackentries.WholeInRange(v.Points, 0, max)
		if !ok {
			return nil, &Error{Code: "invalid_points", Message: fmt.Sprintf("Points for %q must be a whole number from 0 to %d.", v.CriterionID, max)}
		}
		scores = append(scores, entryrepo.JudgeScore{CriterionID: v.CriterionID, Points: pts})
	}
	return scores, nil
}

// TeamComments returns the comments for a team's results.
func (s *Service) TeamComments(ctx context.Context, eventID, teamID string) ([]string, error) {
	return entryrepo.TeamComments(ctx, s.db, eventID, teamID)
}
