package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// SetTeamTrack puts the event's team on trackID (none when not valid).
// sql.ErrNoRows: the team is not the event's.
func SetTeamTrack(ctx context.Context, q Querier, eventID, teamID string, trackID sql.NullString) error {
	result, err := q.ExecContext(ctx, `UPDATE event_teams SET track_id=$3 WHERE event_id=$1 AND id=$2`, eventID, teamID, trackID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TeamTrack is the track the team chose, nil for none (or no such team).
func TeamTrack(ctx context.Context, q Querier, teamID string) (*model.EventTrack, error) {
	team, err := GetEventTeamByID(ctx, q, teamID)
	if err == sql.ErrNoRows || err == nil && !team.TrackID.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tracks, err := hackevents.TracksByIDs(ctx, q, []string{team.TrackID.String})
	if t, ok := tracks[team.TrackID.String]; ok && err == nil {
		return &t, nil
	}
	return nil, err
}

// EventTeamTracks is the track of each of the event's teams that chose one,
// by team id, in two queries.
func EventTeamTracks(ctx context.Context, q Querier, eventID string) (map[string]*model.EventTrack, error) {
	teams, err := ListEventTeams(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	tracks, err := hackevents.ListEventTracks(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.EventTrack{}
	for _, t := range tracks {
		byID[t.ID] = t
	}
	out := map[string]*model.EventTrack{}
	for _, t := range teams {
		if x, ok := byID[t.TrackID.String]; ok && t.TrackID.Valid {
			out[t.ID] = &x
		}
	}
	return out, nil
}
