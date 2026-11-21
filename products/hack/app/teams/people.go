package teams

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

func (s *Service) ListPeople(ctx context.Context, eventID string) ([]model.EventPerson, error) {
	return teamrepo.ListEventPeople(ctx, s.db, eventID)
}

func (s *Service) ListTeams(ctx context.Context, eventID string) ([]model.EventTeam, error) {
	return teamrepo.ListEventTeams(ctx, s.db, eventID)
}

func (s *Service) ListParticipantsOnNoTeam(ctx context.Context, eventID string) ([]model.EventPerson, error) {
	return hackmembers.People(ctx, s.db, hackmembers.Filter{EventID: eventID, Role: "participant", Status: "approved", NoTeam: true})
}

func (s *Service) GetTeam(ctx context.Context, teamID string) (model.EventTeam, error) {
	return teamrepo.GetEventTeamByID(ctx, s.db, teamID)
}

func (s *Service) TeamPeople(ctx context.Context, teamID string) ([]model.EventPerson, error) {
	return teamrepo.ListTeamPeople(ctx, s.db, teamID)
}

// ParticipantTeam reads the current team, roster and finding note for a participant.
func (s *Service) ParticipantTeam(ctx context.Context, team model.EventTeam) (model.EventTeam, []model.EventPerson, string, error) {
	if fresh, err := teamrepo.GetEventTeamByID(ctx, s.db, team.ID); err == nil {
		team = fresh
	}
	people, err := teamrepo.ListTeamPeople(ctx, s.db, team.ID)
	if err != nil {
		return team, nil, "", err
	}
	var note string
	_ = teamrepo.ReadTeamLookingFor(ctx, s.db, team.ID).Scan(&note)
	return team, people, note, nil
}
