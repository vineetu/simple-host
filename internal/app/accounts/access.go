package accounts

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	events "github.com/vsriram/simple-host/internal/repository/hackevents"
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
func (s *Service) TeamWriteState(ctx context.Context, id string) (model.TeamWriteState, error) {
	return events.TeamWriteStateFor(ctx, s.database, id, false)
}

// TeamWriteStateInTx holds FOR SHARE in the caller's transaction; it never opens another transaction.
func (s *Service) TeamWriteStateInTx(ctx context.Context, tx *sql.Tx, id string) (model.TeamWriteState, error) {
	return events.TeamWriteStateFor(ctx, tx, id, true)
}
func (s *Service) FileLinkScopeAllows(ctx context.Context, site model.Site, kind, id, memberID string) bool {
	switch kind {
	case "team":
		team, err := events.ResolveTeamIdentity(ctx, s.database, memberID, id)
		return err == nil && team.AccountID == site.UserID && team.TeamSlug == site.Name
	case "event":
		ev, err := events.GetEventByAccount(ctx, s.database, site.UserID)
		if err != nil || ev.ID != id || site.Name != ev.ID || ev.TakenDown() {
			return false
		}
		member, err := events.GetEventMember(ctx, s.database, ev.ID, memberID)
		return err == nil && member.Role == "organiser"
	}
	return false
}
