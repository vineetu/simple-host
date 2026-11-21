package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

type Event = model.Event

// ErrHackFindingStage indicates that team changes are unavailable at this stage.
var ErrHackFindingStage = errors.New("teams cannot be changed at this stage")

// ErrHackFindingOff indicates that team finding is disabled.
var ErrHackFindingOff = errors.New("team finding is off")

// ErrHackFindingForbidden indicates a forbidden team-finding action.
var ErrHackFindingForbidden = errors.New("this team action is not available")

// LockFindingEvent serializes finding changes with the organiser's settings.
func LockFindingEvent(ctx context.Context, q Querier, eventID string) error {
	_, err := lockFindingEvent(ctx, q, eventID)
	return err
}

// lockFindingEvent reads the event FOR SHARE and refuses when finding is off
// or the stage is past building.
func lockFindingEvent(ctx context.Context, q Querier, eventID string) (Event, error) {
	ev, err := hackevents.GetEventForShare(ctx, q, eventID)
	if err != nil {
		return ev, err
	}
	if !ev.TeamFinding {
		return ev, ErrHackFindingOff
	}
	if ev.Stage != "open" && ev.Stage != "building" {
		return ev, ErrHackFindingStage
	}
	return ev, nil
}

// FindingEvent reads settings under the same event lock used by all finding writes.
func FindingEvent(ctx context.Context, q Querier, ev Event) (Event, error) {
	locked, err := lockFindingEvent(ctx, q, ev.ID)
	if err != nil {
		return ev, err
	}
	ev.TeamSizeMax = locked.TeamSizeMax
	return ev, nil
}

// TeamMember is one person on one team.
type TeamMember struct{ TeamID, UserID string }

// FindingTeamMembers is the approved members of the event's unlocked teams,
// oldest member first.
func FindingTeamMembers(ctx context.Context, q sqlkit.Querier, eventID string) ([]TeamMember, error) {
	unlocked, err := userIDs(ctx, q, `SELECT id FROM event_teams WHERE event_id=$1 AND locked_at IS NULL`, eventID)
	if err != nil {
		return nil, err
	}
	open := map[string]bool{}
	for _, id := range unlocked {
		open[id] = true
	}
	members, err := hackmembers.Members(ctx, q, hackmembers.Filter{EventID: eventID, Status: "approved", OnTeam: true})
	if err != nil {
		return nil, err
	}
	out := []TeamMember{}
	for _, m := range members {
		if open[m.TeamID.String] {
			out = append(out, TeamMember{TeamID: m.TeamID.String, UserID: m.UserID})
		}
	}
	return out, nil
}

// VisibleTeamRequests is the event's requests and invites from or to userID,
// to or from teamID, or all of them for an organiser, oldest first; only
// those whose person is still in the event.
func VisibleTeamRequests(ctx context.Context, q sqlkit.Querier, eventID, userID string, teamID sql.NullString, organiser bool) ([]model.TeamRequest, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,r.kind,r.created_at
		FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id
		WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`, eventID, userID, teamID, organiser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []model.TeamRequest
	var ids []string
	for rows.Next() {
		var p model.TeamRequest
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.Kind, &p.CreatedAt); err != nil {
			return nil, err
		}
		found, ids = append(found, p), append(ids, p.UserID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := hackmembers.MembersByIDs(ctx, q, eventID, ids)
	if err != nil {
		return nil, err
	}
	out := []model.TeamRequest{}
	for _, p := range found {
		if _, ok := members[p.UserID]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// FindingTeamState is whether userID's team teamID is locked, what it is
// looking for and whether userID admins it; sql.ErrNoRows when they are not on it.
func FindingTeamState(ctx context.Context, q sqlkit.Querier, teamID, eventID, userID string) (locked bool, lookingFor string, admin bool, err error) {
	if err = q.QueryRowContext(ctx, `SELECT locked_at IS NOT NULL, looking_for FROM event_teams WHERE id = $1`, teamID).Scan(&locked, &lookingFor); err != nil {
		return false, "", false, err
	}
	m, err := hackmembers.Member(ctx, q, eventID, userID, hackmembers.NoLock)
	if err == nil && m.TeamID.String != teamID {
		err = sql.ErrNoRows
	}
	if err != nil {
		return false, "", false, err
	}
	return locked, lookingFor, m.TeamAdmin, nil
}
