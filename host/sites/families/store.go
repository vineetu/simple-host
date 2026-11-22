package families

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// DeleteFamily removes a family (by id) and returns what it was.
func (svc Service) DeleteFamily(ctx context.Context, id string) (model.AddressFamily, error) {
	return repo.DeleteFamily(ctx, svc.DB, id)
}

// GetFamilyByUserSuffix is one account's family. sql.ErrNoRows if none.
func (svc Service) GetFamilyByUserSuffix(ctx context.Context, userID string, suffix string) (model.AddressFamily, error) {
	return repo.GetFamilyByUserSuffix(ctx, svc.DB, userID, suffix)
}

// ListFamiliesToCheck is every pending family, plus verified ones whose last
// check is older than recheck.
func (svc Service) ListFamiliesToCheck(ctx context.Context, recheck time.Duration) ([]model.AddressFamily, error) {
	return repo.ListFamiliesToCheck(ctx, svc.DB, recheck)
}

// MarkFamilyLapseNotified records that the owner was told their failing
// family will be let go at releaseAt.
func (svc Service) MarkFamilyLapseNotified(ctx context.Context, id string, releaseAt time.Time) error {
	return repo.MarkFamilyLapseNotified(ctx, svc.DB, id, releaseAt)
}

// ReleaseExpiredFamilies removes pending families older than ttl and
// returns them.
func (svc Service) ReleaseExpiredFamilies(ctx context.Context, ttl time.Duration) ([]model.AddressFamily, error) {
	return repo.ReleaseExpiredFamilies(ctx, svc.DB, ttl)
}

// SetFamilyCheck records one check: ok clears any failure; otherwise a
// verified family starts (or keeps) its failing clock and a pending one keeps
// waiting with the reason.
func (svc Service) SetFamilyCheck(ctx context.Context, id string, ok bool, reason string) (model.FamilyCheck, error) {
	return repo.SetFamilyCheck(ctx, svc.DB, id, ok, reason)
}
