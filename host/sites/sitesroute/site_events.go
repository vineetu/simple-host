package sitesroute

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
)

// noteDeployed follows an update that made a new version live: the owner's
// site.deployed event and the deploy count.
func (h *Handler) NoteDeployed(ctx context.Context, user *model.User, siteID string, version, prev int) {
	h.EmitDeployed(siteID, version, prev, "deploy")
	h.CountDeploy(ctx, user)
}

// emitDeployed queues the owner's site.deployed event.
func (h *Handler) EmitDeployed(siteID string, version, prev int, via string) {
	h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: siteID, Name: "site.deployed", Data: map[string]any{"version": version, "previous_version": prev, "via": via}})
}

// countDeploy counts a team member's deploy in the paper metrics.
func (h *Handler) CountDeploy(ctx context.Context, user *model.User) {
	if h.Ed.Identity().PaperMetrics && user.Member != nil {
		papermetrics.From(ctx).Deploy(ctx)
	}
}
