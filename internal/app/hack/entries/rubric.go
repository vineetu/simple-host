package entries

import (
	"context"
	"fmt"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
)

// Rubric returns the event's criteria.
func (s *Service) Rubric(ctx context.Context, eventID string) ([]model.RubricCriterion, error) {
	return entryrepo.ListRubric(ctx, s.db, eventID)
}

// ReplaceRubric checks score deletion and replaces the criteria under the event lock.
func (s *Service) ReplaceRubric(ctx context.Context, eventID string, items []model.RubricCriterion, ids []string, confirm bool) ([]model.RubricCriterion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	if ev.JudgingLockedAt.Valid {
		return nil, &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first to change the rubric."}
	}
	existing, err := entryrepo.ListRubric(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, c := range existing {
		known[c.ID] = true
	}
	kept := map[string]int{}
	for i, id := range ids {
		if id != "" {
			if !known[id] {
				return nil, &Error{Code: "invalid_rubric", Message: "Unknown or repeated criterion."}
			}
			if _, ok := kept[id]; ok {
				return nil, &Error{Code: "invalid_rubric", Message: "Unknown or repeated criterion."}
			}
			items[i].ID = id
			kept[id] = items[i].MaxPoints
		}
	}
	if ev.TieCriterionID.Valid && (ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived") {
		if _, ok := kept[ev.TieCriterionID.String]; !ok {
			return nil, &Error{Code: "tie_rule_closed", Message: "the tie criterion was fixed before judging began"}
		}
	}
	warnings := []string{}
	for _, c := range existing {
		max, keep := kept[c.ID]
		if keep && max >= c.MaxPoints {
			continue
		}
		count, err := entryrepo.CountRubricScoresForDeletion(ctx, tx, eventID, c.ID, !keep, max)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			warnings = append(warnings, fmt.Sprintf("%d scores for %s will be deleted", count, c.Name))
		}
	}
	if len(warnings) > 0 && !confirm {
		return nil, &Error{Code: "score_deletion_confirmation", Message: strings.Join(warnings, "\n")}
	}
	if err := entryrepo.ReplaceRubric(ctx, tx, eventID, items); err != nil {
		return nil, err
	}
	saved, err := entryrepo.ListRubric(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}
