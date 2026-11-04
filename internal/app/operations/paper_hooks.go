package operations

import (
	"context"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"time"
)

// These entry points record the current request measurement without coupling transports to the collector.

func ResolveHackEvent(ctx context.Context, slug, stage, member string, published ...time.Time) {
	papermetrics.ResolveHackEvent(ctx, slug, stage, member, published...)
}
func HackMember(ctx context.Context, member string) { papermetrics.HackMember(ctx, member) }
func ResolveHackSite(ctx context.Context, account, site, member string, write bool) {
	papermetrics.ResolveHackSite(ctx, account, site, member, write)
}
func HackWriteCommitted(ctx context.Context) { papermetrics.HackWriteCommitted(ctx) }
func HackDeploy(ctx context.Context)         { papermetrics.HackDeploy(ctx) }
func HackMutationResult(ctx context.Context, method, path string, status int) {
	papermetrics.HackMutationResult(ctx, method, path, status)
}
func RecordHackStage(ctx context.Context, eventID, stage string, origin ...time.Time) {
	papermetrics.RecordHackStage(ctx, eventID, stage, origin...)
}
func HackPhasesChanged(ctx context.Context) { papermetrics.HackPhasesChanged(ctx) }
