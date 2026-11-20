package accounts

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/accounts"
	authrules "github.com/vsriram/simple-host/host/rules/auth"
)

type Service struct{ database *sql.DB }

// New constructs the shared account authorization service.
func New(database *sql.DB) *Service { return &Service{database: database} }

// GetUserByAPIKey resolves stored keys and in-process credentials without changing their scope.
func (s *Service) GetUserByAPIKey(ctx context.Context, apiKey string, keyIdle time.Duration) (model.User, error) {
	// A member acting for a team (owner=<team>, checked by ScopeGate for this
	// request): every reader of this request's key sees the team's account.
	if u, ok := actingFor(ctx, apiKey); ok {
		return u, nil
	}
	if strings.HasPrefix(apiKey, "shint_") {
		c, ok := repo.LookupInternalCred(apiKey)
		if !ok || c.Visitor != nil {
			return model.User{}, sql.ErrNoRows
		}
		u, err := repo.GetUserByID(ctx, s.database, c.UserID)
		if err == nil && u.Suspended {
			return u, model.ErrAccountSuspended
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
	return user, err
}
