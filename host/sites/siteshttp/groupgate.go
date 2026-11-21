package siteshttp

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
)

// SetGroupGate installs the product's write gate before serving requests.
func (h *SiteHandler) SetGroupGate(g sites.GroupGate) { h.groupGate = g }

// RecordGateSite annotates a held-site write at the transaction gate.
func (h *SiteHandler) RecordGateSite(ctx context.Context, user *model.User, name string) {
	if h.Ed.Identity().PaperMetrics {
		papermetrics.From(ctx).ResolveSite(ctx, user.ID, name, user.Member.PersonID, false)
	}
}

// RecordGateState annotates the write state read by the product's gate.
func (h *SiteHandler) RecordGateState(ctx context.Context, user *model.User, stage string) {
	papermetrics.From(ctx).ResolveGroup(ctx, user.Handle.String, stage, user.Member.PersonID)
}
