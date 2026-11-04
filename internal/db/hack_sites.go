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
