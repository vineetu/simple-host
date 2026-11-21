package entries

import (
	"context"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
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
func (s *Service) UpdateJudgingSettings(ctx context.Context, eventID string, req JudgingSettings) (hackmodel.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return hackmodel.Event{}, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return hackmodel.Event{}, err
	}
	if err := judgingSettingsRefusal(ev, req); err != nil {
		return hackmodel.Event{}, err
	}
	if err := updateOrganiserJudges(ctx, tx, ev.ID, req.OrganiserJudges, req.IncludeOrganisers); err != nil {
		return hackmodel.Event{}, err
	}
	if err := checkTieCriterion(ctx, tx, ev.ID, req.TieCriterionID); err != nil {
		return hackmodel.Event{}, err
	}
	if err := saveJudgingSettings(ctx, tx, ev.ID, req); err != nil {
		return hackmodel.Event{}, err
	}
	if ev, err = eventrepo.GetEventForUpdate(ctx, tx, ev.ID); err != nil {
		return hackmodel.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return hackmodel.Event{}, err
	}
	return ev, nil
}

// judgingStarted are the stages where judging setup and the tie rule are fixed.
var judgingStarted = map[string]bool{"judging": true, "results": true}

// judgingSettingsRefusal refuses changes the event's stage no longer allows.
func judgingSettingsRefusal(ev hackmodel.Event, req JudgingSettings) error {
	setup := req.Mode != nil || req.PerTeam != nil || req.OrganiserJudges != nil || req.IncludeOrganisers != nil
	switch {
	case ev.Stage == "archived":
		return errEventClosed
	case setup && ev.JudgingLockedAt.Valid:
		return errScoresLocked
	case setup && judgingStarted[ev.Stage]:
		return errSetupLocked
	case req.TieCriterionID != nil && judgingStarted[ev.Stage]:
		return &Error{Code: "tie_rule_closed", Message: "choose the tie criterion before judging begins"}
	}
	return nil
}

// checkTieCriterion refuses a tie criterion from another event; nil or ""
// needs no check.
func checkTieCriterion(ctx context.Context, q sqlkit.Querier, eventID string, id *string) error {
	if id == nil || *id == "" {
		return nil
	}
	valid, err := entryrepo.EventCriterionInEvent(ctx, q, eventID, *id)
	if err == nil && !valid {
		return &Error{Code: "criterion_not_found", Message: "criterion not in this event"}
	}
	return err
}

// saveJudgingSettings writes the assignment settings and the score settings
// that the request changes.
func saveJudgingSettings(ctx context.Context, q sqlkit.Querier, eventID string, req JudgingSettings) error {
	if req.Mode != nil || req.PerTeam != nil {
		if _, err := eventrepo.UpdateJudgingSettings(ctx, q, eventID, req.Mode, req.PerTeam); err != nil {
			return err
		}
	}
	if req.ScoreMode == nil && req.TieCriterionID == nil && req.PublicScores == nil && req.PublicRanks == nil {
		return nil
	}
	return eventrepo.UpdateScoreSettings(ctx, q, eventID, valueOrNil(req.ScoreMode), req.TieCriterionID != nil, valueOrNil(req.TieCriterionID), valueOrNil(req.PublicScores), valueOrNil(req.PublicRanks))
}

// valueOrNil passes an unset optional setting to SQL as NULL.
func valueOrNil[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
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
