package hackteams

import (
	"context"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
)

// Event aliases the shared model record.
type Event = model.Event

// ErrHackFindingStage indicates that team changes are unavailable at this stage.
var ErrHackFindingStage = errors.New("teams cannot be changed at this stage")

// ErrHackFindingOff indicates that team finding is disabled.
var ErrHackFindingOff = errors.New("team finding is off")

// ErrHackFindingForbidden indicates a forbidden team-finding action.
var ErrHackFindingForbidden = errors.New("this team action is not available")

// LockFindingEvent serializes finding changes with the organiser's settings.
func LockFindingEvent(ctx context.Context, q Querier, eventID string) error {
	var enabled bool
	var stage string
	if err := q.QueryRowContext(ctx, `SELECT team_finding,stage FROM events WHERE id=$1 FOR SHARE`, eventID).Scan(&enabled, &stage); err != nil {
		return err
	}
	if !enabled {
		return ErrHackFindingOff
	}
	if stage != "open" && stage != "building" {
		return ErrHackFindingStage
	}
	return nil
}

// FindingEvent reads settings under the same event lock used by all finding writes.
func FindingEvent(ctx context.Context, q Querier, ev Event) (Event, error) {
	if err := LockFindingEvent(ctx, q, ev.ID); err != nil {
		return ev, err
	}
	err := q.QueryRowContext(ctx, "SELECT team_size_max FROM events WHERE id=$1", ev.ID).Scan(&ev.TeamSizeMax)
	return ev, err
}
