package access

import (
	"context"
	"errors"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
	"time"
)

// ConnectorAccess resolves an access token to its person, the connection's
// mode ("events" for a product connection, teamConnections set) and its grant.
func (s *Service) ConnectorAccess(ctx context.Context, token string, now func() time.Time, teamConnections bool, audiences ...string) (model.User, string, model.OAuthTokenGrant, bool) {
	tok, err := s.GetOAuthToken(ctx, hashSecret(token))
	if err != nil || tok.Kind != "access" || now().After(tok.ExpiresAt) {
		return model.User{}, "", model.OAuthTokenGrant{}, false
	}
	audienceOK := false
	for _, a := range audiences {
		if tok.Resource == a {
			audienceOK = true
		}
	}
	if !audienceOK {
		return model.User{}, "", model.OAuthTokenGrant{}, false
	}
	user, err := s.GetUserByID(ctx, tok.UserID)
	// No "has an API key" check any more: keys are stored hashed and the
	// connector does not replay one (it acts through a per-request internal
	// credential), so an account with no key yet can still use its connection.
	if err != nil || user.Username == "admin" {
		return model.User{}, "", model.OAuthTokenGrant{}, false
	}
	// A suspended person's token is refused but the grant is kept, so
	// re-enabling the account brings the connection back. The user is
	// returned (with Suspended set) so callers can say why.
	if user.Suspended {
		return user, "", model.OAuthTokenGrant{}, false
	}
	_ = s.TouchOAuthGrant(ctx, tok.GrantID)
	if !teamConnections {
		return user, "", tok, true
	}
	if !authrules.EventsGrant(tok.Scope) {
		return model.User{}, "", model.OAuthTokenGrant{}, false
	}
	return user, "events", tok, true
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
	return s.InternalKeyFor(u.ID)
}
