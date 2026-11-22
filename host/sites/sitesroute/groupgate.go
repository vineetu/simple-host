package sitesroute

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *Handler) SetGroupGate(g sites.GroupGate) { h.groupGate = g }

func (h *Handler) RecordGateSite(ctx context.Context, user *model.User, name string) {
	if h.Ed.Identity().PaperMetrics {
		papermetrics.From(ctx).ResolveSite(ctx, user.ID, name, user.Member.PersonID, false)
	}
}

func (h *Handler) RecordGateState(ctx context.Context, user *model.User, stage string) {
	papermetrics.From(ctx).ResolveGroup(ctx, user.Handle.String, stage, user.Member.PersonID)
}
