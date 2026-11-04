package accounts

import (
	"context"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	authrules "github.com/vsriram/simple-host/internal/rules/auth"
	"time"
)

func (s *Service) ConnectorAccess(ctx context.Context, token string, now func() time.Time, hackTeams bool, audiences ...string) (model.User, string, string, model.OAuthTokenGrant, bool) {
	tok, err := s.GetOAuthToken(ctx, hashSecret(token))
	if err != nil || tok.Kind != "access" || now().After(tok.ExpiresAt) {
		return model.User{}, "", "", model.OAuthTokenGrant{}, false
	}
	audienceOK := false
	for _, a := range audiences {
		if tok.Resource == a {
			audienceOK = true
		}
	}
	if !audienceOK {
		return model.User{}, "", "", model.OAuthTokenGrant{}, false
	}
	user, err := s.GetUserByID(ctx, tok.UserID)
	// No "has an API key" check any more: keys are stored hashed and the
	// connector does not replay one (it acts through a per-request internal
	// credential), so an account with no key yet can still use its connection.
	if err != nil || user.Username == "admin" {
		return model.User{}, "", "", model.OAuthTokenGrant{}, false
	}
	// A suspended person's token is refused but the grant is kept, so
	// re-enabling the account brings the connection back. The user is
	// returned (with Suspended set) so callers can say why.
	if user.Suspended {
		return user, "", "", model.OAuthTokenGrant{}, false
	}
	_ = s.TouchOAuthGrant(ctx, tok.GrantID)
	if !hackTeams {
		return user, authrules.ScopeTeam(tok.Scope), "", tok, true
	}
	teamID, events, ok := authrules.GrantKind(tok.Scope)
	if !ok {
		return model.User{}, "", "", model.OAuthTokenGrant{}, false
	}
	if events {
		return user, "", "events", tok, true
	}
	return user, teamID, "", tok, true
}

// PhotoCredential revalidates the connection and principal before minting a request credential.
func (s *Service) PhotoCredential(ctx context.Context, grantID, userID, clientID, resource string) (string, func(), error) {
	current, err := s.GetOAuthGrantByID(ctx, grantID)
	if err != nil || current.UserID != userID || current.ClientID != clientID || current.Resource != resource {
		return "", nil, errors.New("photo sign-in ended")
	}
	u, err := s.GetUserByID(ctx, current.UserID)
	if err != nil || u.Suspended || u.Username == "admin" {
		return "", nil, errors.New("photo sign-in ended")
	}
	return s.InternalKeyFor(u.ID, authrules.ScopeTeam(current.Scope))
}
