package access

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// VisitorSessionStart is a visitor session just written: its cookie value's
// raw ID, when it lapses unused, and whether it is the person's first
// sign-in on the site.
type VisitorSessionStart struct {
	ID            []byte
	IdleExpiresAt time.Time
	First         bool
}

// StartVisitorSession signs userID in to siteID on host for ttl, sliding
// idle. A suspended account gets no session (model.ErrAccountSuspended).
func (s *Service) StartVisitorSession(ctx context.Context, userID, siteID, host string, ttl, idle time.Duration) (VisitorSessionStart, error) {
	susp, err := repo.UserSuspended(ctx, s.database, userID)
	if err != nil {
		return VisitorSessionStart{}, err
	}
	if susp {
		return VisitorSessionStart{}, model.ErrAccountSuspended
	}
	id, err := providerRandomRaw(32)
	if err != nil {
		return VisitorSessionStart{}, err
	}
	now := time.Now()
	start := VisitorSessionStart{ID: id, IdleExpiresAt: now.Add(idle)}
	start.First, err = repo.InsertVisitorSession(ctx, s.database, id, userID, siteID, host, now.Add(ttl), start.IdleExpiresAt)
	return start, err
}
