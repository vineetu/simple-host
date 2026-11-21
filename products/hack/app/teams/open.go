package teams

import (
	"context"
	"database/sql"
	"errors"

	accounts "github.com/vsriram/simple-host/host/access"
	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type OpenTeam struct {
	id                     string
	Slug, Name, LookingFor string
	Members                []string
	Track                  *model.EventTrack
}

type SoloPerson struct{ UserID, DisplayName, Intro string }

type FindingTeamState struct {
	LookingFor    string
	Locked, Admin bool
}

type FindingDirectory struct {
	Teams   []OpenTeam
	Solo    []SoloPerson
	Pending []model.TeamRequest
	Looking bool
	Intro   string
	Team    *FindingTeamState
}

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
		solo, err := collect(rows, err, func(r *sql.Rows, p *SoloPerson) error {
			return r.Scan(&p.UserID, &p.Intro)
		})
		if err != nil {
			return out, err
		}
		if out.Solo, err = named(ctx, s, solo, func(p *SoloPerson) (string, *string) { return p.UserID, &p.DisplayName }); err != nil {
			return out, err
		}
	}
	rows, err := teamrepo.ListVisibleTeamRequests(ctx, s.db, eventID, userID, member.TeamID, organiser)
	pending, err := collect(rows, err, func(r *sql.Rows, p *model.TeamRequest) error {
		return r.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.Kind, &p.CreatedAt)
	})
	if err != nil {
		return out, err
	}
	if out.Pending, err = named(ctx, s, pending, func(p *model.TeamRequest) (string, *string) { return p.UserID, &p.DisplayName }); err != nil {
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
	type teamMember struct{ teamID, userID, name string }
	rows, err = teamrepo.ListFindingTeamMembers(ctx, s.db, eventID)
	members, err := collect(rows, err, func(r *sql.Rows, m *teamMember) error {
		return r.Scan(&m.teamID, &m.userID)
	})
	if err != nil {
		return teams, err
	}
	if members, err = named(ctx, s, members, func(m *teamMember) (string, *string) { return m.userID, &m.name }); err != nil {
		return teams, err
	}
	for _, m := range members {
		if t := byID[m.teamID]; t != nil {
			t.Members = append(t.Members, m.name)
		}
	}
	return teams, nil
}

// named fills each row's display name from its account (field gives the
// row's account id and where its name goes), in one query, leaving out rows
// whose account is gone.
func named[T any](ctx context.Context, s *Service, rows []T, field func(*T) (string, *string)) ([]T, error) {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i], _ = field(&rows[i])
	}
	users, err := accounts.UsersByIDs(ctx, s.db, ids)
	if err != nil {
		return rows, err
	}
	out := rows[:0]
	for i := range rows {
		id, name := field(&rows[i])
		if u, ok := users[id]; ok {
			*name = u.DisplayName.String
			out = append(out, rows[i])
		}
	}
	return out, nil
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
