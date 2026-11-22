package connector

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func DeleteOAuthGrant(ctx context.Context, db *sql.DB, grantID string) error {
	return repo.DeleteOAuthGrant(ctx, db, grantID)
}
func DeleteOAuthGrantsForClient(ctx context.Context, db *sql.DB, userID string, clientID string) (int64, error) {
	return repo.DeleteOAuthGrantsForClient(ctx, db, userID, clientID)
}
func DeleteOAuthToken(ctx context.Context, db *sql.DB, tokenHash string) error {
	return repo.DeleteOAuthToken(ctx, db, tokenHash)
}
func GetOAuthClient(ctx context.Context, db *sql.DB, clientID string) (model.OAuthClient, error) {
	return repo.GetOAuthClient(ctx, db, clientID)
}
func GetOAuthGrantByID(ctx context.Context, db *sql.DB, grantID string) (model.OAuthGrant, error) {
	return repo.GetOAuthGrantByID(ctx, db, grantID)
}
func GetOAuthToken(ctx context.Context, db *sql.DB, tokenHash string) (model.OAuthTokenGrant, error) {
	return repo.GetOAuthToken(ctx, db, tokenHash)
}
func InsertOAuthClient(ctx context.Context, db *sql.DB, c model.OAuthClient) error {
	return repo.InsertOAuthClient(ctx, db, c)
}
func InsertOAuthCode(ctx context.Context, db *sql.DB, codeHash string, c model.OAuthCode) error {
	return repo.InsertOAuthCode(ctx, db, codeHash, c)
}
func SetOAuthClientRedirectURIs(ctx context.Context, db *sql.DB, clientID string, uris []string) error {
	return repo.SetOAuthClientRedirectURIs(ctx, db, clientID, uris)
}
func SweepOAuth(ctx context.Context, db *sql.DB, unusedClientAge time.Duration) (int64, error) {
	return repo.SweepOAuth(ctx, db, unusedClientAge)
}
func TouchOAuthClient(ctx context.Context, db *sql.DB, clientID string) error {
	return repo.TouchOAuthClient(ctx, db, clientID)
}
func TouchOAuthGrant(ctx context.Context, db *sql.DB, grantID string) error {
	return repo.TouchOAuthGrant(ctx, db, grantID)
}
func UpsertBuiltinOAuthClient(ctx context.Context, db *sql.DB, clientID string, name string, redirectURIs []string) error {
	return repo.UpsertBuiltinOAuthClient(ctx, db, clientID, name, redirectURIs)
}

func PurgeExpiredAPIKeys(ctx context.Context, db *sql.DB, idle time.Duration) (int64, error) {
	return repo.PurgeExpiredAPIKeys(ctx, db, idle)
}

// NamespaceDomains returns the namespace configured by the composition root.
