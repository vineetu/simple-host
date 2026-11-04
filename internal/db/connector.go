package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"time"
)

type OAuthClient = model.OAuthClient
type OAuthCode = model.OAuthCode
type OAuthGrant = model.OAuthGrant
type OAuthTokenGrant = model.OAuthTokenGrant
type OAuthConnection = model.OAuthConnection

var ErrOAuthCodeUsed = model.ErrOAuthCodeUsed

func ConsumeOAuthCode(ctx context.Context, q Querier, codeHash string) (OAuthCode, error) {
	return repo.ConsumeOAuthCode(ctx, q, codeHash)
}
func DeleteOAuthClient(ctx context.Context, q Querier, clientID string) (bool, error) {
	return repo.DeleteOAuthClient(ctx, q, clientID)
}
func DeleteOAuthGrant(ctx context.Context, q Querier, grantID string) error {
	return repo.DeleteOAuthGrant(ctx, q, grantID)
}
func DeleteOAuthGrantsForClient(ctx context.Context, q Querier, userID, clientID string) (int64, error) {
	return repo.DeleteOAuthGrantsForClient(ctx, q, userID, clientID)
}
func DeleteOAuthGrantsForUser(ctx context.Context, q Querier, userID string) error {
	return repo.DeleteOAuthGrantsForUser(ctx, q, userID)
}
func DeleteOAuthToken(ctx context.Context, q Querier, tokenHash string) error {
	return repo.DeleteOAuthToken(ctx, q, tokenHash)
}
func GetOAuthClient(ctx context.Context, q Querier, clientID string) (OAuthClient, error) {
	return repo.GetOAuthClient(ctx, q, clientID)
}
func GetOAuthGrantByID(ctx context.Context, q Querier, grantID string) (OAuthGrant, error) {
	return repo.GetOAuthGrantByID(ctx, q, grantID)
}
func GetOAuthToken(ctx context.Context, q Querier, tokenHash string) (OAuthTokenGrant, error) {
	return repo.GetOAuthToken(ctx, q, tokenHash)
}
func InsertOAuthClient(ctx context.Context, q Querier, c OAuthClient) error {
	return repo.InsertOAuthClient(ctx, q, c)
}
func InsertOAuthCode(ctx context.Context, q Querier, codeHash string, c OAuthCode) error {
	return repo.InsertOAuthCode(ctx, q, codeHash, c)
}
func InsertOAuthGrant(ctx context.Context, q Querier, userID, clientID, scope, resource, device string) (string, error) {
	return repo.InsertOAuthGrant(ctx, q, userID, clientID, scope, resource, device)
}
func InsertOAuthToken(ctx context.Context, q Querier, tokenHash, grantID, kind string, expiresAt time.Time) error {
	return repo.InsertOAuthToken(ctx, q, tokenHash, grantID, kind, expiresAt)
}
func ListOAuthConnections(ctx context.Context, database *sql.DB, userID string) ([]OAuthConnection, error) {
	return repo.ListOAuthConnections(ctx, database, userID)
}
func ListOperatorOAuthClients(ctx context.Context, database *sql.DB) ([]OAuthClient, error) {
	return repo.ListOperatorOAuthClients(ctx, database)
}
func MarkRefreshTokenUsed(ctx context.Context, q Querier, tokenHash string) (bool, error) {
	return repo.MarkRefreshTokenUsed(ctx, q, tokenHash)
}
func SelectOAuthTeam(ctx context.Context, q Querier, grantID, userID, teamID string) (bool, error) {
	return repo.SelectOAuthTeam(ctx, q, grantID, userID, teamID)
}
func SetOAuthClientRedirectURIs(ctx context.Context, q Querier, clientID string, uris []string) error {
	return repo.SetOAuthClientRedirectURIs(ctx, q, clientID, uris)
}
func SetOAuthCodeGrant(ctx context.Context, q Querier, codeHash, grantID string) error {
	return repo.SetOAuthCodeGrant(ctx, q, codeHash, grantID)
}
func SweepOAuth(ctx context.Context, database *sql.DB, unusedClientAge time.Duration) (int64, error) {
	return repo.SweepOAuth(ctx, database, unusedClientAge)
}
func TouchOAuthClient(ctx context.Context, q Querier, clientID string) error {
	return repo.TouchOAuthClient(ctx, q, clientID)
}
func TouchOAuthGrant(ctx context.Context, q Querier, grantID string) error {
	return repo.TouchOAuthGrant(ctx, q, grantID)
}
func UpsertBuiltinOAuthClient(ctx context.Context, q Querier, clientID, name string, redirectURIs []string) error {
	return repo.UpsertBuiltinOAuthClient(ctx, q, clientID, name, redirectURIs)
}
