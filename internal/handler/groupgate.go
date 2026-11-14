package handler

import (
	"context"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

// SetGroupGate installs the product's write gate before serving requests.
func (h *SiteHandler) SetGroupGate(g sites.GroupGate) { h.groupGate = g }

// RecordGateSite annotates a held-site write at the transaction gate.
func (h *SiteHandler) RecordGateSite(ctx context.Context, user *model.User, name string) {
	if h.ed.Identity().PaperMetrics {
		paperRecorder(ctx).ResolveSite(ctx, user.Team.AccountID, name, user.Team.MemberID, false)
	}
}

// RecordGateState annotates the write state read by the product's gate.
func (h *SiteHandler) RecordGateState(ctx context.Context, user *model.User, stage string) {
	paperRecorder(ctx).ResolveGroup(ctx, user.Team.EventSlug, stage, user.Team.MemberID)
}
