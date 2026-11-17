package accounts

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/accounts"
)

func (s *Service) GetUserByID(ctx context.Context, id string) (model.User, error) {
	return repo.GetUserByID(ctx, s.database, id)
}
func (s *Service) GetLatestOAuthIdentity(ctx context.Context, id string) (model.OAuthIdentity, error) {
	return repo.GetLatestOAuthIdentity(ctx, s.database, id)
}
func (s *Service) GetVisitorSession(ctx context.Context, id []byte, idle time.Duration) (model.VisitorSession, error) {
	return repo.GetVisitorSession(ctx, s.database, id, idle)
}
func (s *Service) UserSuspended(ctx context.Context, id string) (bool, error) {
	return repo.UserSuspended(ctx, s.database, id)
}
func (s *Service) APIKeyScope(ctx context.Context, key string, idle time.Duration) (string, error) {
	return repo.APIKeyScope(ctx, s.database, key, idle)
}
func (s *Service) TouchVisitorSession(ctx context.Context, id []byte, idle time.Duration) error {
	return repo.TouchVisitorSession(ctx, s.database, id, idle)
}

// GroupWriteRefusal asks the installed product at the scope gate's write check.
func (s *Service) GroupWriteRefusal(ctx context.Context, id string) (int, string, string, error) {
	if groupHooks.Keys == nil {
		return 0, "", "", nil
	}
	return groupHooks.Keys.WriteRefusal(ctx, s.database, id)
}

func (s *Service) GroupKeyMessages() (scopeOnly, inactive string) {
	if groupHooks.Keys == nil {
		return "", ""
	}
	return groupHooks.Keys.Messages()
}

func (s *Service) FileLinkScopeAllows(ctx context.Context, site model.Site, kind, id, memberID string) bool {
	return groupHooks.Keys != nil && groupHooks.Keys.FileLinkAllows(ctx, s.database, site, kind, id, memberID)
}
