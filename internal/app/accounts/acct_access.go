package accounts

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"time"
)

func (s *Service) InsertVisitorSession(ctx context.Context, id []byte, userID, siteID, host string, expiresAt, idleExpiresAt time.Time) (bool, error) {
	return repo.InsertVisitorSession(ctx, s.database, id, userID, siteID, host, expiresAt, idleExpiresAt)
}
func (s *Service) ActivateVisitorSession(ctx context.Context, id []byte, ttl, idle time.Duration) (bool, error) {
	return repo.ActivateVisitorSession(ctx, s.database, id, ttl, idle)
}
func (s *Service) DeleteVisitorSession(ctx context.Context, id []byte) error {
	return repo.DeleteVisitorSession(ctx, s.database, id)
}
func (s *Service) ConsumeEstablishToken(ctx context.Context, once, host string) (model.EstablishToken, error) {
	return repo.ConsumeEstablishToken(ctx, s.database, once, host)
}
func (s *Service) IssueInternalVisitorKey(pass model.InternalVisitorPass) (string, func(), error) {
	return repo.IssueInternalVisitorKey(pass)
}
func (s *Service) LookupInternalVisitorKey(key string) (model.InternalVisitorPass, bool) {
	return repo.LookupInternalVisitorKey(key)
}

func (s *Service) ListHandleAliases(ctx context.Context, id string) ([]string, error) {
	return repo.ListHandleAliases(ctx, s.database, id)
}
func (s *Service) ListAPIKeys(ctx context.Context, id string) ([]model.APIKey, error) {
	return repo.ListAPIKeys(ctx, s.database, id)
}
