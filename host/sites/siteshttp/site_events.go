package siteshttp

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
)

// noteDeployed follows an update that made a new version live: the owner's
// site.deployed event and the deploy count.
func (h *SiteHandler) noteDeployed(ctx context.Context, user *model.User, result sites.UpdateResult) {
	h.emitDeployed(result.Site.ID, result.Version, result.PreviousVersion, "deploy")
	h.countDeploy(ctx, user)
}

// emitDeployed queues the owner's site.deployed event.
func (h *SiteHandler) emitDeployed(siteID string, version, prev int, via string) {
	h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: siteID, Name: "site.deployed", Data: map[string]any{"version": version, "previous_version": prev, "via": via}})
}

// countDeploy counts a team member's deploy in the paper metrics.
func (h *SiteHandler) countDeploy(ctx context.Context, user *model.User) {
	if h.Ed.Identity().PaperMetrics && user.Member != nil {
		papermetrics.From(ctx).Deploy(ctx)
	}
}
