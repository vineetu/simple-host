package entries

import (
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"

	"context"
	"fmt"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"strings"
)

func (s *Service) Rubric(ctx context.Context, eventID string) ([]model.RubricCriterion, error) {
	return entryrepo.ListRubric(ctx, s.db, eventID)
}

// ReplaceRubric checks score deletion and replaces the criteria under the event lock.
func (s *Service) ReplaceRubric(ctx context.Context, eventID string, items []model.RubricCriterion, ids []string, confirm bool, actorID ...string) ([]model.RubricCriterion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	if err := rubricLocked(ev); err != nil {
		return nil, err
	}
	existing, err := entryrepo.ListRubric(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	kept, err := keptCriteria(existing, items, ids)
	if err != nil {
		return nil, err
	}
	if _, ok := kept[ev.TieCriterionID.String]; ev.TieCriterionID.Valid && tieRuleFixed[ev.Stage] && !ok {
		return nil, &Error{Code: "tie_rule_closed", Message: "the tie criterion was fixed before judging began"}
	}
	warnings, err := scoreDeletionWarnings(ctx, tx, eventID, existing, kept)
	if err != nil {
		return nil, err
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

// tieRuleFixed are the stages after which the tie criterion must stay.
var tieRuleFixed = map[string]bool{"judging": true, "results": true, "archived": true}

// rubricLocked refuses rubric changes once judging has started or is locked.
func rubricLocked(ev model.Event) error {
	if ev.JudgingLockedAt.Valid {
		return &Error{Code: "scores_locked", Message: "Judging is locked because the event is at Results. Move the event back to Entries closed to change the rubric."}
	}
	if ev.Stage == "judging" {
		return errSetupLocked
	}
	return nil
}

// keptCriteria gives items[i] the existing criterion ids[i] names and returns
// each kept criterion's new maximum; an empty id is a new criterion.
func keptCriteria(existing, items []model.RubricCriterion, ids []string) (map[string]int, error) {
	known := map[string]bool{}
	for _, c := range existing {
		known[c.ID] = true
	}
	kept := map[string]int{}
	for i, id := range ids {
		if id == "" {
			continue
		}
		if _, repeated := kept[id]; repeated || !known[id] {
			return nil, &Error{Code: "invalid_rubric", Message: "Unknown or repeated criterion."}
		}
		items[i].ID = id
		kept[id] = items[i].MaxPoints
	}
	return kept, nil
}

// scoreDeletionWarnings counts the scores lost to removed criteria or lowered
// maximums, one line per affected criterion.
func scoreDeletionWarnings(ctx context.Context, q sqlkit.Querier, eventID string, existing []model.RubricCriterion, kept map[string]int) ([]string, error) {
	warnings := []string{}
	for _, c := range existing {
		max, keep := kept[c.ID]
		if keep && max >= c.MaxPoints {
			continue
		}
		count, err := entryrepo.CountRubricScoresForDeletion(ctx, q, eventID, c.ID, !keep, max)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			warnings = append(warnings, fmt.Sprintf("%d scores for %s will be deleted", count, c.Name))
		}
	}
	return warnings, nil
}

func RubricVersion(criteria []model.RubricCriterion) string {
	return rules.RubricFingerprint(criteria)
}
