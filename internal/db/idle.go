package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"time"
)

type IdleExempt = repo.IdleExempt
type IdleSite = repo.IdleSite
type IdleLink = model.IdleLink
type SiteIdleFlags = model.SiteIdleFlags

func CancelIdleLinks(ctx context.Context, q Querier, userID string) error {
	return repo.CancelIdleLinks(ctx, q, userID)
}
func ClearStaleIdleWarnings(ctx context.Context, database *sql.DB, ex IdleExempt) (int64, error) {
	return repo.ClearStaleIdleWarnings(ctx, database, ex)
}
func GetIdleLink(ctx context.Context, database *sql.DB, tokenHash []byte) (IdleLink, error) {
	return repo.GetIdleLink(ctx, database, tokenHash)
}
func IdleEvidence(ctx context.Context, database *sql.DB) (since, lastIngest time.Time, err error) {
	return repo.IdleEvidence(ctx, database)
}
func KeepIdleSite(ctx context.Context, q Querier, siteID string) error {
	return repo.KeepIdleSite(ctx, q, siteID)
}
func ListIdleSitesToRemove(ctx context.Context, database *sql.DB, ex IdleExempt, now time.Time, grace time.Duration, limit int) ([]IdleSite, error) {
	return repo.ListIdleSitesToRemove(ctx, database, ex, now, grace, limit)
}
func ListIdleSitesToWarn(ctx context.Context, database *sql.DB, ex IdleExempt, idleBefore time.Time, limit int) ([]IdleSite, error) {
	return repo.ListIdleSitesToWarn(ctx, database, ex, idleBefore, limit)
}
func ListSiteIdleFlags(ctx context.Context, database *sql.DB, userID string) (map[string]SiteIdleFlags, error) {
	return repo.ListSiteIdleFlags(ctx, database, userID)
}
func MarkIdleRemoved(ctx context.Context, q Querier, ex IdleExempt, siteID string, tokenHash []byte, now time.Time, grace time.Duration) error {
	return repo.MarkIdleRemoved(ctx, q, ex, siteID, tokenHash, now, grace)
}
func MarkIdleWarned(ctx context.Context, tx *sql.Tx, ex IdleExempt, siteID string, tokenHash []byte, idleBefore, removeAt time.Time) error {
	return repo.MarkIdleWarned(ctx, tx, ex, siteID, tokenHash, idleBefore, removeAt)
}
func SetSiteKeep(ctx context.Context, database *sql.DB, siteID string, keep bool) error {
	return repo.SetSiteKeep(ctx, database, siteID, keep)
}
