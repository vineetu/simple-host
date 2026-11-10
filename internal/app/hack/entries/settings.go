package entries

import (
	"context"

	model "github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// JudgingSettings contains the optional settings to change.
type JudgingSettings struct {
	OrganiserJudges           *[]string
	IncludeOrganisers         *bool
	Mode                      *string
	PerTeam                   *int
	ScoreMode, TieCriterionID *string
	PublicScores, PublicRanks *bool
}

// UpdateJudgingSettings applies settings under the event lock.
func (s *Service) UpdateJudgingSettings(ctx context.Context, eventID string, req JudgingSettings) (model.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return model.Event{}, err
	}
	if ev.Stage == "archived" {
		return model.Event{}, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if (req.Mode != nil || req.PerTeam != nil || req.OrganiserJudges != nil || req.IncludeOrganisers != nil) && ev.JudgingLockedAt.Valid {
		return model.Event{}, &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first."}
	}
	if (req.Mode != nil || req.PerTeam != nil || req.OrganiserJudges != nil || req.IncludeOrganisers != nil) && (ev.Stage == "judging" || ev.Stage == "results") {
		return model.Event{}, &Error{Code: "judging_setup_locked", Message: "Judging has started. Move the event back to Entries closed to change this."}
	}
	if err := updateOrganiserJudges(ctx, tx, ev.ID, req.OrganiserJudges, req.IncludeOrganisers); err != nil {
		return model.Event{}, err
	}
	if req.TieCriterionID != nil {
		if ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived" {
			return model.Event{}, &Error{Code: "tie_rule_closed", Message: "choose the tie criterion before judging begins"}
		}
		if *req.TieCriterionID != "" {
			valid, err := entryrepo.EventCriterionInEvent(ctx, tx, ev.ID, *req.TieCriterionID)
			if err != nil {
				return model.Event{}, err
			}
			if !valid {
				return model.Event{}, &Error{Code: "criterion_not_found", Message: "criterion not in this event"}
			}
		}
	}
	if req.Mode != nil || req.PerTeam != nil {
		ev, err = eventrepo.UpdateJudgingSettings(ctx, tx, ev.ID, req.Mode, req.PerTeam)
		if err != nil {
			return model.Event{}, err
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
		err = eventrepo.UpdateScoreSettings(ctx, tx, ev.ID, scoreMode, req.TieCriterionID != nil, tie, publicScores, publicRanks)
		if err != nil {
			return model.Event{}, err
		}
	}
	ev, err = eventrepo.GetEventForUpdate(ctx, tx, ev.ID)
	if err != nil {
		return model.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, err
	}
	return ev, nil
}

// SetJudgingLock sets or clears the event's judging lock.
func (s *Service) SetJudgingLock(ctx context.Context, eventID string, locked bool, reason string) (model.Event, error) {
	return eventrepo.SetJudgingLock(ctx, s.db, eventID, locked, reason)
}

func updateOrganiserJudges(ctx context.Context, q sqlkit.Querier, eventID string, selection *[]string, alias *bool) error {
	if selection == nil && alias == nil {
		return nil
	}
	ids := []string{}
	if selection != nil {
		ids = *selection
	} else if *alias {
		var err error
		ids, err = eventrepo.OrganiserIDs(ctx, q, eventID)
		if err != nil {
			return err
		}
	}
	for _, id := range ids {
		if !model.ValidUUID(id) {
			return &Error{Code: "invalid_organiser_judges", Message: "Choose current organisers of this event."}
		}
	}
	valid, err := eventrepo.SetOrganiserJudges(ctx, q, eventID, ids)
	if err != nil {
		return err
	}
	if !valid {
		return &Error{Code: "invalid_organiser_judges", Message: "Choose current organisers of this event."}
	}
	return nil
}
