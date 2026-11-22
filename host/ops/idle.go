package ops

import (
	"context"
	"github.com/vsriram/simple-host/host/sites/repo"
	"time"
)

func (s *Reporting) IdleEvidence(ctx context.Context) (since, lastIngest time.Time, err error) {
	return repo.IdleEvidence(ctx, s.database)
}
func (s *Reporting) ListIdleSitesToRemove(ctx context.Context, ex repo.IdleExempt, idleBefore time.Time, limit int) ([]repo.IdleSite, error) {
	return repo.ListIdleSitesToRemove(ctx, s.database, ex, idleBefore, limit)
}
