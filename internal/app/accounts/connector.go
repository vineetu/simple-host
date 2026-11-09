package accounts

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	events "github.com/vsriram/simple-host/internal/repository/hackevents"
	"time"
)

func (s *Service) ConsumeOAuthCode(ctx context.Context, codeHash string) (model.OAuthCode, error) {
	return repo.ConsumeOAuthCode(ctx, s.database, codeHash)
}
func (s *Service) DeleteOAuthClient(ctx context.Context, clientID string) (bool, error) {
	return repo.DeleteOAuthClient(ctx, s.database, clientID)
}
func (s *Service) DeleteOAuthGrant(ctx context.Context, grantID string) error {
	return repo.DeleteOAuthGrant(ctx, s.database, grantID)
}
func (s *Service) DeleteOAuthGrantsForClient(ctx context.Context, userID string, clientID string) (int64, error) {
	return repo.DeleteOAuthGrantsForClient(ctx, s.database, userID, clientID)
}
func (s *Service) DeleteOAuthGrantsForUser(ctx context.Context, userID string) error {
	return repo.DeleteOAuthGrantsForUser(ctx, s.database, userID)
}
func (s *Service) DeleteOAuthToken(ctx context.Context, tokenHash string) error {
	return repo.DeleteOAuthToken(ctx, s.database, tokenHash)
}
func (s *Service) GetOAuthClient(ctx context.Context, clientID string) (model.OAuthClient, error) {
	return repo.GetOAuthClient(ctx, s.database, clientID)
}
func (s *Service) GetOAuthGrantByID(ctx context.Context, grantID string) (model.OAuthGrant, error) {
	return repo.GetOAuthGrantByID(ctx, s.database, grantID)
}
func (s *Service) GetOAuthToken(ctx context.Context, tokenHash string) (model.OAuthTokenGrant, error) {
	return repo.GetOAuthToken(ctx, s.database, tokenHash)
}
func (s *Service) InsertOAuthClient(ctx context.Context, c model.OAuthClient) error {
	return repo.InsertOAuthClient(ctx, s.database, c)
}
func (s *Service) InsertOAuthCode(ctx context.Context, codeHash string, c model.OAuthCode) error {
	return repo.InsertOAuthCode(ctx, s.database, codeHash, c)
}
func (s *Service) InsertOAuthGrant(ctx context.Context, userID string, clientID string, scope string, resource string, device string) (string, error) {
	return repo.InsertOAuthGrant(ctx, s.database, userID, clientID, scope, resource, device)
}
func (s *Service) InsertOAuthToken(ctx context.Context, tokenHash string, grantID string, kind string, expiresAt time.Time) error {
	return repo.InsertOAuthToken(ctx, s.database, tokenHash, grantID, kind, expiresAt)
}
func (s *Service) ListOAuthConnections(ctx context.Context, userID string) ([]model.OAuthConnection, error) {
	return repo.ListOAuthConnections(ctx, s.database, userID)
}
func (s *Service) ListOperatorOAuthClients(ctx context.Context) ([]model.OAuthClient, error) {
	return repo.ListOperatorOAuthClients(ctx, s.database)
}
func (s *Service) MarkRefreshTokenUsed(ctx context.Context, tokenHash string) (bool, error) {
	return repo.MarkRefreshTokenUsed(ctx, s.database, tokenHash)
}
func (s *Service) SelectOAuthTeam(ctx context.Context, grantID string, userID string, teamID string) (bool, error) {
	return repo.SelectOAuthTeam(ctx, s.database, grantID, userID, teamID)
}
func (s *Service) SetOAuthClientRedirectURIs(ctx context.Context, clientID string, uris []string) error {
	return repo.SetOAuthClientRedirectURIs(ctx, s.database, clientID, uris)
}
func (s *Service) SetOAuthCodeGrant(ctx context.Context, codeHash string, grantID string) error {
	return repo.SetOAuthCodeGrant(ctx, s.database, codeHash, grantID)
}
func (s *Service) SweepOAuth(ctx context.Context, unusedClientAge time.Duration) (int64, error) {
	return repo.SweepOAuth(ctx, s.database, unusedClientAge)
}
func (s *Service) TouchOAuthClient(ctx context.Context, clientID string) error {
	return repo.TouchOAuthClient(ctx, s.database, clientID)
}
func (s *Service) TouchOAuthGrant(ctx context.Context, grantID string) error {
	return repo.TouchOAuthGrant(ctx, s.database, grantID)
}
func (s *Service) UpsertBuiltinOAuthClient(ctx context.Context, clientID string, name string, redirectURIs []string) error {
	return repo.UpsertBuiltinOAuthClient(ctx, s.database, clientID, name, redirectURIs)
}
func (s *Service) ResolveTeamIdentity(ctx context.Context, member, team string) (model.TeamIdentity, error) {
	return events.ResolveTeamIdentity(ctx, s.database, member, team)
}

func (s *Service) InternalKeyFor(userID, teamID string) (string, func(), error) {
	if teamID != "" {
		return repo.IssueInternalTeamKey(userID, teamID)
	}
	return repo.IssueInternalKey(userID)
}

func (s *Service) PurgeExpiredAPIKeys(ctx context.Context, idle time.Duration) (int64, error) {
	return repo.PurgeExpiredAPIKeys(ctx, s.database, idle)
}

// NamespaceDomains returns the namespace configured by the composition root.
func (s *Service) NamespaceDomains() []string { return repo.PlatformDomains() }

func (s *Service) OAuthConnectionTeams(ctx context.Context, userID string) (map[string][]model.OAuthConnectionTeam, error) {
	return repo.OAuthConnectionTeams(ctx, s.database, userID)
}
