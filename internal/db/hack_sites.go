package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
)

// Team sites, member keys and the submission deadline (hosted events, M2;
// db/migrations/hack2-team-sites.sql, docs/designs/simple-hack-platform.md).
//
// A team's site is the site named after the team (event_teams.slug) of the
// event's holding account (events.account_id). A member deploys with a key of
// scope KeyScopeTeam, which belongs to the member and acts as the holding
// account on that one site, only while the member is a participant on that
// team.

// KeyScopeTeam is a member's key for their team's site.
// KeyScopeTeam is used by internal/auth.
const KeyScopeTeam = hackteams.KeyScopeTeam

// TeamIdentity is what a team credential acts as.
// TeamIdentity is used by internal/auth.
type TeamIdentity = model.TeamIdentity

// TODO(4.3): Host dependency
// actAsTeam turns an authenticated member into the holding account acting on
// the team's site: the caller's own identity is kept in Team.MemberID.
func actAsTeam(ctx context.Context, q *sql.DB, member User, id TeamIdentity) (User, error) {
	holder, err := GetUserByID(ctx, q, id.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, hackteams.ErrTeamKeyInactive
		}
		return User{}, err
	}
	if holder.Suspended {
		// The platform took the event down: its team keys stop entirely.
		return holder, ErrAccountSuspended
	}
	holder.IsAdmin = false
	holder.KeyHash = member.KeyHash
	holder.KeyScope = KeyScopeTeam
	holder.KeyExpiresAt = member.KeyExpiresAt
	// The person's own suspension stops their key; the holding account is
	// never suspended by hand (the event's take-down is checked per deploy).
	holder.Suspended = member.Suspended
	holder.SuspendedReason = member.SuspendedReason
	t := id
	holder.Team = &t
	return holder, nil
}

// TODO(4.3): Host dependency
// TeamScopeOfKey answers the scope gate for a team credential: whether apiKey
// is one (a stored key of scope team, or an internal credential bound to a
// team) and, if so, what it acts as right now (live=false when the person is
// no longer on that team: every route is then refused).
func TeamScopeOfKey(ctx context.Context, q Querier, apiKey string) (isTeam, live bool, id TeamIdentity, err error) {
	memberID, teamID, internal := lookupInternalTeamKey(apiKey)
	if !internal {
		var scope string
		err = q.QueryRowContext(ctx, `SELECT scope FROM api_keys WHERE key_hash = $1`, HashAPIKey(apiKey)).Scan(&scope)
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, id, nil
		}
		if err != nil || scope != KeyScopeTeam {
			return false, false, id, err
		}
		teamID, memberID, err = hackteams.TeamKeyBinding(ctx, q, HashAPIKey(apiKey))
		if errors.Is(err, sql.ErrNoRows) {
			return true, false, id, nil
		}
		if err != nil {
			return true, false, id, err
		}
	}
	id, err = ResolveTeamIdentity(ctx, q, memberID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, false, id, nil
	}
	return true, err == nil, id, err
}

// OrphanTeamSites lists live sites of events' holding accounts whose team no
// longer exists (the team was removed or emptied): the sweep takes them to
// Recently deleted. Hack SQL is kept in db only because it scans Host Site rows;
// it moves with Site in 4.3.
func OrphanTeamSites(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, accountID string) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.active_version
		  FROM sites s JOIN events e ON e.account_id = s.user_id
		 WHERE s.deleted_at IS NULL
		   AND ($1 = '' OR s.user_id::text = $1)
		   AND s.name <> e.id::text
		   AND NOT EXISTS (SELECT 1 FROM event_teams t WHERE t.event_id = e.id AND t.slug = s.name)`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.ActiveVersion); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
