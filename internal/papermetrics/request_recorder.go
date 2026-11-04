package papermetrics

import (
	"context"
	"time"
)

// RequestRecorder records transport-resolved facts in the current collector request.
// Without a collector request in the context these methods do nothing.
type RequestRecorder struct{}

func (RequestRecorder) ResolveHackEvent(ctx context.Context, slug, stage, member string, published ...time.Time) {
	ResolveHackEvent(ctx, slug, stage, member, published...)
}
func (RequestRecorder) HackMember(ctx context.Context, member string) { HackMember(ctx, member) }
func (RequestRecorder) ResolveHackSite(ctx context.Context, account, site, member string, write bool) {
	ResolveHackSite(ctx, account, site, member, write)
}
func (RequestRecorder) HackWriteCommitted(ctx context.Context) { HackWriteCommitted(ctx) }
func (RequestRecorder) HackDeploy(ctx context.Context)         { HackDeploy(ctx) }
func (RequestRecorder) HackMutationResult(ctx context.Context, method, path string, status int) {
	HackMutationResult(ctx, method, path, status)
}
func (RequestRecorder) RecordHackStage(ctx context.Context, eventID, stage string, origin ...time.Time) {
	RecordHackStage(ctx, eventID, stage, origin...)
}
func (RequestRecorder) HackPhasesChanged(ctx context.Context) { HackPhasesChanged(ctx) }
