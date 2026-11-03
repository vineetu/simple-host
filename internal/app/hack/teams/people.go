package teams

import (
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"

	"context"
	"github.com/vsriram/simple-host/internal/db"
)

// ListPeople reads event people and the set of key holders.
func (s *Service) ListPeople(ctx context.Context, eventID string) ([]db.EventPerson, map[string]bool, error) {
	people, err := eventrepo.ListEventPeople(ctx, s.db, eventID)
	if err != nil {
		return nil, nil, err
	}
	keyed, err := db.EventKeyHolders(ctx, s.db, eventID)
	if err != nil {
		return nil, nil, err
	}
	return people, keyed, nil
}

// ListTeams reads the event teams in their existing order.
func (s *Service) ListTeams(ctx context.Context, eventID string) ([]db.EventTeam, error) {
	return teamrepo.ListEventTeams(ctx, s.db, eventID)
}

// ListParticipantsOnNoTeam reads participants without a team.
func (s *Service) ListParticipantsOnNoTeam(ctx context.Context, eventID string) ([]db.EventPerson, error) {
	return eventrepo.ListParticipantsOnNoTeam(ctx, s.db, eventID)
}

// GetTeam reads a team by ID.
func (s *Service) GetTeam(ctx context.Context, teamID string) (db.EventTeam, error) {
	return teamrepo.GetEventTeamByID(ctx, s.db, teamID)
}

// TeamPeople reads a team's roster.
func (s *Service) TeamPeople(ctx context.Context, teamID string) ([]db.EventPerson, error) {
	return teamrepo.ListTeamPeople(ctx, s.db, teamID)
}

// ParticipantTeam reads the current team, roster and finding note for a participant.
func (s *Service) ParticipantTeam(ctx context.Context, team db.EventTeam) (db.EventTeam, []db.EventPerson, string, error) {
	if fresh, err := teamrepo.GetEventTeamByID(ctx, s.db, team.ID); err == nil {
		team = fresh
	}
	people, err := teamrepo.ListTeamPeople(ctx, s.db, team.ID)
	if err != nil {
		return team, nil, "", err
	}
	var note string
	_ = teamrepo.RunPeopleSQL1(ctx, s.db, team.ID).Scan(&note)
	return team, people, note, nil
}
