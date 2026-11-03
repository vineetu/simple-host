package teams

import (
	model "github.com/vsriram/simple-host/internal/model"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"

	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
)

// OpenTeam is a team shown in the finding directory.
type OpenTeam struct {
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
	Pending []db.TeamRequest
	Looking bool
	Intro   string
	Team    *FindingTeamState
}

// ListOpenTeams reads the finding directory visible to a member.
func (s *Service) ListOpenTeams(ctx context.Context, eventID, userID string, member db.EventMember) (FindingDirectory, error) {
	out := FindingDirectory{Teams: []OpenTeam{}, Solo: []SoloPerson{}, Pending: []db.TeamRequest{}}
	// Solo participants see rosters; team members see opted-in solo people.
	// Frozen teams cannot accept newcomers and must not advertise as open.
	// All unlocked teams are askable, including former code-only teams.
	if !member.TeamID.Valid || member.Role == "organiser" {
		rows, err := teamrepo.ListFindingTeams(ctx, s.db, eventID)
		if err != nil {
			return out, err
		}
		byID := map[string]int{}
		for rows.Next() {
			var id string
			var t OpenTeam
			var track model.EventTrack
			if err = rows.Scan(&id, &t.Slug, &t.Name, &t.LookingFor, &track.ID, &track.Slug, &track.Name, &track.Challenge, &track.Prize); err != nil {
				rows.Close()
				return out, err
			}
			if track.ID != "" {
				t.Track = &track
			}
			t.Members = []string{}
			byID[id] = len(out.Teams)
			out.Teams = append(out.Teams, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		rows, err = teamrepo.ListFindingTeamMembers(ctx, s.db, eventID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				return out, err
			}
			if i, ok := byID[id]; ok {
				out.Teams[i].Members = append(out.Teams[i].Members, name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	if member.TeamID.Valid || member.Role == "organiser" {
		rows, err := teamrepo.ListLookingParticipants(ctx, s.db, eventID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var p SoloPerson
			if err = rows.Scan(&p.UserID, &p.DisplayName, &p.Intro); err != nil {
				rows.Close()
				return out, err
			}
			out.Solo = append(out.Solo, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	rows, err := teamrepo.ListVisibleTeamRequests(ctx, s.db, eventID, userID, member.TeamID, member.Role == "organiser")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p db.TeamRequest
		if err = rows.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.DisplayName, &p.Kind, &p.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Pending = append(out.Pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = teamrepo.ReadMemberLooking(ctx, s.db, eventID, userID).Scan(&out.Looking, &out.Intro); err != nil {
		return out, err
	}
	if member.TeamID.Valid {
		var t FindingTeamState
		if err = teamrepo.ReadFindingTeamState(ctx, s.db, member.TeamID, eventID, userID).Scan(&t.Locked, &t.LookingFor, &t.Admin); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		out.Team = &t
	}
	return out, nil
}
