package teams

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
	rulesauth "github.com/vsriram/simple-host/internal/rules/auth"
	"time"
)

// hackKeyMaxAge is how long a team key's own last use still counts as
// "using a key" on the Website screen, matching the build plan's 30 days.
const hackKeyMaxAge = 30 * 24 * time.Hour

// ConnGrant is one person's latest personal connector connection for the
// Simple Hack MCP resource (scope "sites events"; a legacy team-bound grant,
// scope "sites team:<uuid>", carries no selected_team_id and is not
// "matching" here -- it never appears in this map).
type ConnGrant struct {
	SelectedTeamID sql.NullString
	LastUsedAt     time.Time
	AppName        string
}

// LatestEventsGrantsByUser returns, for each of userIDs that has one, their
// single most-recently-used personal connector grant for resource (an
// "events" grant: the kind that carries selected_team_id). Rows are read
// newest-last-used first and the first matching row per user wins, so a
// user's older events-scope grant is still found even if their very latest
// grant is a stale legacy "team:" one.
func LatestEventsGrantsByUser(ctx context.Context, q *sql.DB, userIDs []string, resource string) (map[string]ConnGrant, error) {
	out := map[string]ConnGrant{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := hackteams.EventsGrants(ctx, q, userIDs, resource)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, g := range rows {
		if seen[g.UserID] {
			continue
		}
		if _, events, ok := rulesauth.GrantKind(g.Scope); !ok || !events {
			continue
		}
		seen[g.UserID] = true
		out[g.UserID] = ConnGrant{SelectedTeamID: g.SelectedTeamID, LastUsedAt: g.LastUsedAt, AppName: g.ClientName}
	}
	return out, nil

}

// LatestGrants reads the latest connector grants for the given users.
func (s *Service) LatestGrants(ctx context.Context, userIDs []string, resource string) (map[string]ConnGrant, error) {
	return LatestEventsGrantsByUser(ctx, s.db, userIDs, resource)
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
	LastUsedAt *time.Time
	OtherTeam  *OtherTeam
	Teammates  []string
}

// ConnectionStatus reads the caller's connection and current teammates.
func (s *Service) ConnectionStatus(ctx context.Context, eventID, teamID, callerID, resource string) (ConnectionStatus, error) {
	out := ConnectionStatus{State: "none", Teammates: []string{}}
	people, err := db.ListTeamPeople(ctx, s.db, teamID)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(people))
	for _, p := range people {
		if p.Role == "participant" && p.ApprovalStatus == "approved" {
			ids = append(ids, p.UserID)
		}
	}
	latest, err := s.LatestGrants(ctx, ids, resource)
	if err != nil {
		return out, err
	}
	for _, p := range people {
		if p.Role != "participant" || p.ApprovalStatus != "approved" {
			continue
		}
		g, ok := latest[p.UserID]
		if p.UserID == callerID {
			if !ok || !g.SelectedTeamID.Valid {
				continue
			}
			out.LastUsedAt = &g.LastUsedAt
			out.App = g.AppName
			if g.SelectedTeamID.String == teamID {
				out.State = "connected"
			} else {
				out.State = "other_team"
				if event, team, ok := s.OtherTeamDisplay(ctx, g.SelectedTeamID.String); ok {
					out.OtherTeam = &OtherTeam{Event: event, Team: team}
				}
			}
			continue
		}
		if ok && g.SelectedTeamID.Valid && g.SelectedTeamID.String == teamID {
			out.Teammates = append(out.Teammates, p.DisplayName)
		}
	}
	if out.State == "none" {
		if k, err := db.GetTeamKey(ctx, s.db, eventID, callerID); err == nil && k.TeamID == teamID && k.LastUsedAt != nil && time.Since(*k.LastUsedAt) <= hackKeyMaxAge {
			out.State = "key"
			out.LastUsedAt = k.LastUsedAt
		}
	}
	return out, nil
}
