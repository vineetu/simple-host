package teams

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/repository/hackteams"
	rulesauth "github.com/vsriram/simple-host/internal/rules/auth"
)

// hackKeyMaxAge is how long a team key's own last use still counts as
// "using a key" on the Website screen, matching the build plan's 30 days.
const hackKeyMaxAge = 30 * 24 * time.Hour

// ConnGrant is one personal events connector grant for the Simple Hack resource.
type ConnGrant struct {
	SelectedTeamID sql.NullString
	LastUsedAt     time.Time
	AppName        string
}

// EventsGrantsByUser returns all events grants per user, most recently used first.
func EventsGrantsByUser(ctx context.Context, q *sql.DB, userIDs []string, resource string) (map[string][]ConnGrant, error) {
	out := map[string][]ConnGrant{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := hackteams.EventsGrants(ctx, q, userIDs, resource)
	if err != nil {
		return nil, err
	}
	for _, g := range rows {
		if _, events, ok := rulesauth.GrantKind(g.Scope); !ok || !events {
			continue
		}
		out[g.UserID] = append(out[g.UserID], ConnGrant{SelectedTeamID: g.SelectedTeamID, LastUsedAt: g.LastUsedAt, AppName: g.ClientName})
	}
	return out, nil
}

// OtherTeamDisplay reads the selected team's display names.
func (s *Service) OtherTeamDisplay(ctx context.Context, teamID string) (string, string, bool) {
	return hackteams.OtherTeamDisplay(ctx, s.db, teamID)
}

// OtherTeam identifies the team selected in a connector grant.
type OtherTeam struct{ Event, Team string }

// ConnectionStatus is the caller's connection and connected teammates.
type ConnectionStatus struct {
	State, App string
	Apps       []string
	LastUsedAt *time.Time
	OtherTeam  *OtherTeam
	Teammates  []string
}

// ConnectionStatus reads the caller's connection and current teammates.
func (s *Service) ConnectionStatus(ctx context.Context, eventID, teamID, callerID, resource string) (ConnectionStatus, error) {
	out := ConnectionStatus{State: "none", Apps: []string{}, Teammates: []string{}}
	people, err := hackteams.ListTeamPeople(ctx, s.db, teamID)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(people))
	for _, p := range people {
		if p.Role == "participant" && p.ApprovalStatus == "approved" {
			ids = append(ids, p.UserID)
		}
	}
	grants, err := EventsGrantsByUser(ctx, s.db, ids, resource)
	if err != nil {
		return out, err
	}
	for _, p := range people {
		if p.Role != "participant" || p.ApprovalStatus != "approved" {
			continue
		}
		if p.UserID == callerID {
			buckets := map[string][]ConnGrant{}
			otherTeams := map[string]*OtherTeam{}
			for _, g := range grants[p.UserID] {
				state := "no_team"
				if g.SelectedTeamID.Valid {
					id := g.SelectedTeamID.String
					if id == teamID {
						state = "connected"
					} else {
						if _, cached := otherTeams[id]; !cached {
							otherTeams[id] = nil
							if event, team, ok := s.OtherTeamDisplay(ctx, id); ok {
								otherTeams[id] = &OtherTeam{Event: event, Team: team}
							}
						}
						if otherTeams[id] != nil {
							state = "other_team"
						}
					}
				}
				buckets[state] = append(buckets[state], g)
			}
			// Grants are newest first. Prefer this team, then no team, then another team.
			for _, state := range []string{"connected", "no_team", "other_team"} {
				for _, g := range buckets[state] {
					if out.State == "none" {
						out.State, out.App = state, g.AppName
						used := g.LastUsedAt
						out.LastUsedAt = &used
						if state == "other_team" {
							out.OtherTeam = otherTeams[g.SelectedTeamID.String]
						}
					}
					out.Apps = append(out.Apps, g.AppName)
					if state == "other_team" {
						break
					}
				}
				if out.State != "none" {
					break
				}
			}
			continue
		}
		for _, g := range grants[p.UserID] {
			if g.SelectedTeamID.Valid && g.SelectedTeamID.String == teamID {
				out.Teammates = append(out.Teammates, p.DisplayName)
				break
			}
		}
	}

	if out.State == "none" {
		if k, err := hackteams.GetTeamKey(ctx, s.db, eventID, callerID); err == nil && k.TeamID == teamID && k.LastUsedAt != nil && time.Since(*k.LastUsedAt) <= hackKeyMaxAge {
			out.State = "key"
			out.LastUsedAt = k.LastUsedAt
		}
	}
	return out, nil
}
