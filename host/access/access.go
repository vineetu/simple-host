package access

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) GetUserByID(ctx context.Context, id string) (model.User, error) {
	return repo.GetUserByID(ctx, s.database, id)
}
func (s *Service) GetLatestOAuthIdentity(ctx context.Context, id string) (model.OAuthIdentity, error) {
	return repo.GetLatestOAuthIdentity(ctx, s.database, id)
}

// VisitorSession is the one answer to "who is the visitor signed in on
// host": the session with raw id, live (absolute and idle expiry, its SSO
// parent key still live), made on host for siteID ("" for any site on that
// host), of an account that is not suspended. sql.ErrNoRows otherwise.
func (s *Service) VisitorSession(ctx context.Context, id []byte, host, siteID string, idle time.Duration) (model.VisitorSession, error) {
	sess, err := repo.GetVisitorSession(ctx, s.database, id, idle)
	if err != nil {
		return model.VisitorSession{}, err
	}
	now := time.Now()
	if !strings.EqualFold(sess.Host, host) || (siteID != "" && sess.SiteID != siteID) || now.After(sess.ExpiresAt) || now.After(sess.IdleExpiresAt) {
		return model.VisitorSession{}, sql.ErrNoRows
	}
	if susp, err := repo.UserSuspended(ctx, s.database, sess.UserID); err != nil {
		return model.VisitorSession{}, err
	} else if susp {
		return model.VisitorSession{}, sql.ErrNoRows
	}
	return sess, nil
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
