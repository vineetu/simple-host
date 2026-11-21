package teams

import (
	"context"
	"database/sql"
	"errors"

	accounts "github.com/vsriram/simple-host/host/access"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
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
		looking, err := hackmembers.Members(ctx, s.db, hackmembers.Filter{EventID: eventID, Role: "participant", Status: "approved", NoTeam: true, Looking: true})
		if err != nil {
			return out, err
		}
		solo := []SoloPerson{}
		for _, m := range looking {
			solo = append(solo, SoloPerson{UserID: m.UserID, Intro: m.LookingIntro})
		}
		if out.Solo, err = named(ctx, s, solo, func(p *SoloPerson) (string, *string) { return p.UserID, &p.DisplayName }); err != nil {
			return out, err
		}
	}
	pending, err := teamrepo.VisibleTeamRequests(ctx, s.db, eventID, userID, member.TeamID, organiser)
	if err != nil {
		return out, err
	}
	if out.Pending, err = named(ctx, s, pending, func(p *model.TeamRequest) (string, *string) { return p.UserID, &p.DisplayName }); err != nil {
		return out, err
	}
	me, err := hackmembers.Member(ctx, s.db, eventID, userID, hackmembers.NoLock)
	if err != nil {
		return out, err
	}
	out.Looking, out.Intro = me.Looking, me.LookingIntro
	if member.TeamID.Valid {
		out.Team, err = s.findingTeamState(ctx, eventID, userID, member.TeamID)
	}
	return out, err
}

func (s *Service) openTeams(ctx context.Context, eventID string) ([]OpenTeam, error) {
	tracks, err := eventrepo.ListEventTracks(ctx, s.db, eventID)
	if err != nil {
		return nil, err
	}
	rows, err := teamrepo.ListFindingTeams(ctx, s.db, eventID)
	teams, err := collect(rows, err, func(r *sql.Rows, t *OpenTeam) error {
		var trackID string
		err := r.Scan(&t.id, &t.Slug, &t.Name, &t.LookingFor, &trackID)
		for i := range tracks {
			if tracks[i].ID == trackID {
				t.Track = &tracks[i]
			}
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
	onTeams, err := teamrepo.FindingTeamMembers(ctx, s.db, eventID)
	if err != nil {
		return teams, err
	}
	members := make([]teamMember, len(onTeams))
	for i, m := range onTeams {
		members[i] = teamMember{teamID: m.TeamID, userID: m.UserID}
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
	var err error
	t.Locked, t.LookingFor, t.Admin, err = teamrepo.FindingTeamState(ctx, s.db, teamID.String, eventID, userID)
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
