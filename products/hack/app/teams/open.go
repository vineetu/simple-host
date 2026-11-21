package teams

import (
	"context"
	"database/sql"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// OpenTeam is a team shown in the finding directory.
type OpenTeam struct {
	id                     string
	Slug, Name, LookingFor string
	Members                []string
	Track                  *model.EventTrack
}

// SoloPerson is an opted-in participant without a team.
type SoloPerson struct{ UserID, DisplayName, Intro string }

// FindingTeamState describes the caller's team.
type FindingTeamState struct {
	LookingFor    string
	Locked, Admin bool
}

// FindingDirectory contains the results of the finding reads.
type FindingDirectory struct {
	Teams   []OpenTeam
	Solo    []SoloPerson
	Pending []model.TeamRequest
	Looking bool
	Intro   string
	Team    *FindingTeamState
}

// ListOpenTeams reads the finding directory a member sees; empty when team finding is off.
func (s *Service) ListOpenTeams(ctx context.Context, ev model.Event, userID string, member model.EventMember) (out FindingDirectory, err error) {
	if !ev.TeamFinding {
		return out, nil
	}
	eventID := ev.ID
	organiser := member.Role == "organiser"
	out = FindingDirectory{Teams: []OpenTeam{}, Solo: []SoloPerson{}}
	if !member.TeamID.Valid || organiser {
		if out.Teams, err = s.openTeams(ctx, eventID); err != nil {
			return out, err
		}
	}
	if member.TeamID.Valid || organiser {
		rows, err := teamrepo.ListLookingParticipants(ctx, s.db, eventID)
		if out.Solo, err = collect(rows, err, func(r *sql.Rows, p *SoloPerson) error {
			return r.Scan(&p.UserID, &p.DisplayName, &p.Intro)
		}); err != nil {
			return out, err
		}
	}
	rows, err := teamrepo.ListVisibleTeamRequests(ctx, s.db, eventID, userID, member.TeamID, organiser)
	if out.Pending, err = collect(rows, err, func(r *sql.Rows, p *model.TeamRequest) error {
		return r.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.DisplayName, &p.Kind, &p.CreatedAt)
	}); err != nil {
		return out, err
	}
	if err = teamrepo.ReadMemberLooking(ctx, s.db, eventID, userID).Scan(&out.Looking, &out.Intro); err != nil {
		return out, err
	}
	if member.TeamID.Valid {
		out.Team, err = s.findingTeamState(ctx, eventID, userID, member.TeamID)
	}
	return out, err
}

// openTeams lists the unlocked teams with their members' names.
func (s *Service) openTeams(ctx context.Context, eventID string) ([]OpenTeam, error) {
	rows, err := teamrepo.ListFindingTeams(ctx, s.db, eventID)
	teams, err := collect(rows, err, func(r *sql.Rows, t *OpenTeam) error {
		var track model.EventTrack
		err := r.Scan(&t.id, &t.Slug, &t.Name, &t.LookingFor, &track.ID, &track.Slug, &track.Name, &track.Challenge, &track.Prize)
		if track.ID != "" {
			t.Track = &track
		}
		t.Members = []string{}
		return err
	})
	if err != nil {
		return teams, err
	}
	byID := make(map[string]*OpenTeam, len(teams))
	for i := range teams {
		byID[teams[i].id] = &teams[i]
	}
	rows, err = teamrepo.ListFindingTeamMembers(ctx, s.db, eventID)
	_, err = collect(rows, err, func(r *sql.Rows, _ *struct{}) error {
		var id, name string
		err := r.Scan(&id, &name)
		if t := byID[id]; t != nil && err == nil {
			t.Members = append(t.Members, name)
		}
		return err
	})
	return teams, err
}

// findingTeamState reads the caller's own team for the finding page; a team
// gone since the membership was read shows as the zero state.
func (s *Service) findingTeamState(ctx context.Context, eventID, userID string, teamID sql.NullString) (*FindingTeamState, error) {
	var t FindingTeamState
	err := teamrepo.ReadFindingTeamState(ctx, s.db, teamID, eventID, userID).Scan(&t.Locked, &t.LookingFor, &t.Admin)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return &t, err
}

// collect scans every row of a query into a non-nil slice and closes it.
func collect[T any](rows *sql.Rows, err error, scan func(*sql.Rows, *T) error) ([]T, error) {
	out := []T{}
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
