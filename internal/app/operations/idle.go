package operations

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"time"
)

func (s *Reporting) CancelIdleLinks(ctx context.Context, userID string) error {
	return repo.CancelIdleLinks(ctx, s.database, userID)
}
func (s *Reporting) ClearStaleIdleWarnings(ctx context.Context, ex repo.IdleExempt) (int64, error) {
	return repo.ClearStaleIdleWarnings(ctx, s.database, ex)
}
func (s *Reporting) GetIdleLink(ctx context.Context, tokenHash []byte) (model.IdleLink, error) {
	return repo.GetIdleLink(ctx, s.database, tokenHash)
}
func (s *Reporting) IdleEvidence(ctx context.Context) (since, lastIngest time.Time, err error) {
	return repo.IdleEvidence(ctx, s.database)
}
func (s *Reporting) KeepIdleSite(ctx context.Context, siteID string) error {
	return repo.KeepIdleSite(ctx, s.database, siteID)
}
func (s *Reporting) ListIdleSitesToRemove(ctx context.Context, ex repo.IdleExempt, now time.Time, grace time.Duration, limit int) ([]repo.IdleSite, error) {
	return repo.ListIdleSitesToRemove(ctx, s.database, ex, now, grace, limit)
}
func (s *Reporting) ListIdleSitesToWarn(ctx context.Context, ex repo.IdleExempt, idleBefore time.Time, limit int) ([]repo.IdleSite, error) {
	return repo.ListIdleSitesToWarn(ctx, s.database, ex, idleBefore, limit)
}
func (s *Reporting) ListSiteIdleFlags(ctx context.Context, userID string) (map[string]model.SiteIdleFlags, error) {
	return repo.ListSiteIdleFlags(ctx, s.database, userID)
}
func (s *Reporting) SetSiteKeep(ctx context.Context, siteID string, keep bool) error {
	return repo.SetSiteKeep(ctx, s.database, siteID, keep)
}
