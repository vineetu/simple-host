package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	events "github.com/vsriram/simple-host/internal/repository/hackevents"
	authrules "github.com/vsriram/simple-host/internal/rules/auth"
)

type Service struct{ database *sql.DB }

// New constructs the shared account authorization service.
func New(database *sql.DB) *Service { return &Service{database: database} }

// GetUserByAPIKey resolves stored keys and in-process credentials without changing their scope.
func (s *Service) GetUserByAPIKey(ctx context.Context, apiKey string, keyIdle time.Duration) (model.User, error) {
	if strings.HasPrefix(apiKey, "shint_") {
		c, ok := repo.LookupInternalCred(apiKey)
		if !ok || c.Visitor != nil {
			return model.User{}, sql.ErrNoRows
		}
		u, err := repo.GetUserByID(ctx, s.database, c.UserID)
		if err == nil && u.Suspended {
			return u, model.ErrAccountSuspended
		}
		if err == nil && c.TeamID != "" {
			// A connector connection bound to a team site (hack_sites.go).
			id, terr := events.ResolveTeamIdentity(ctx, s.database, c.UserID, c.TeamID)
			if terr != nil {
				return model.User{}, terr
			}
			return s.actAsTeam(ctx, u, id)
		}
		return u, err
	}
	user, expiresAt, idleFrom, lastUsed, now, err := repo.KeyPrincipal(ctx, s.database, apiKey)
	if err == nil && user.KeyScope == authrules.KeyScopeDeploy {
		// A deploy key never carries admin powers, even when an admin
		// minted it: a leaked CI secret must not list every site on the
		// instance or skip the site quota.
		user.IsAdmin = false
	}
	if err == nil {
		user.KeyExpiresAt = expiresAt
		// An expired key is kept (the owner sees it in the Keys panel as
		// expired and can revoke it) but never authenticates.
		switch authrules.KeyExpired(now, expiresAt, idleFrom, lastUsed, keyIdle) {
		case authrules.KeyExpiredFixed:
			return user, model.ErrKeyExpired
		case authrules.KeyExpiredIdle:
			return user, model.ErrKeyExpiredIdle
		}
	}
	if err == nil && user.Suspended {
		return user, model.ErrAccountSuspended
	}
	if err == nil {
		repo.TouchAPIKey(ctx, s.database, user.KeyHash)
	}
	if err == nil && user.KeyScope == authrules.KeyScopeTeam {
		// A member's team key: the event's holding account on the team's
		// site, only while they are on that team (hack_sites.go).
		teamID, memberID, terr := events.TeamKeyBinding(ctx, s.database, user.KeyHash)
		if terr == nil && memberID != user.ID {
			terr = events.ErrTeamKeyInactive
		}
		if terr != nil {
			return model.User{}, terr
		}
		id, terr := events.ResolveTeamIdentity(ctx, s.database, memberID, teamID)
		if terr != nil {
			return model.User{}, terr
		}
		return s.actAsTeam(ctx, user, id)
	}
	return user, err
}

func (s *Service) actAsTeam(ctx context.Context, member model.User, id model.TeamIdentity) (model.User, error) {
	holder, err := repo.GetUserByID(ctx, s.database, id.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, events.ErrTeamKeyInactive
		}
		return model.User{}, err
	}
	if holder.Suspended {
		// The platform took the event down: its team keys stop entirely.
		return holder, model.ErrAccountSuspended
	}
	holder.IsAdmin = false
	holder.KeyHash = member.KeyHash
	holder.KeyScope = authrules.KeyScopeTeam
	holder.KeyExpiresAt = member.KeyExpiresAt
	// The person's own suspension stops their key; the holding account is
	// never suspended by hand (the event's take-down is checked per deploy).
	holder.Suspended = member.Suspended
	holder.SuspendedReason = member.SuspendedReason
	t := id
	holder.Team = &t
	return holder, nil
}

// TeamScopeOfKey checks whether a credential is team-bound and still has a live participant.
func (s *Service) TeamScopeOfKey(ctx context.Context, apiKey string) (isTeam, live bool, id model.TeamIdentity, err error) {
	memberID, teamID, internal := repo.LookupInternalTeamKey(apiKey)
	if !internal {
		scope, err := repo.StoredKeyScope(ctx, s.database, apiKey)
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, id, nil
		}
		if err != nil || scope != authrules.KeyScopeTeam {
			return false, false, id, err
		}

		teamID, memberID, err = events.TeamKeyBinding(ctx, s.database, repo.HashAPIKey(apiKey))
		if errors.Is(err, sql.ErrNoRows) {
			return true, false, id, nil
		}
		if err != nil {
			return true, false, id, err
		}
	}
	id, err = events.ResolveTeamIdentity(ctx, s.database, memberID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, false, id, nil
	}
	return true, err == nil, id, err
}
