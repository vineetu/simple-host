package servertest

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

func GroupTestEdition() *edition.Edition { return groupTestEditionWithLimits(config.DefaultLimits()) }
func groupTestEditionWithLimits(limits config.Limits) *edition.Edition {
	return edition.FromPolicies(edition.RoutePolicy{HostedEvents: true}, edition.PresentationPolicy{ProductName: "Test product", ProductAssets: true, CanonicalBaseURL: "https://product.example", StylePath: "product.css", StyleClass: "product"}, edition.IdentityPolicy{RandomHandles: true, PaperMetrics: true}, edition.StoragePolicy{EventSites: true, StrictArchives: true, JudgePreviewFrames: true, EventVersionRetention: true, CombinedPool: true}, edition.NotificationPolicy{EventScoped: true}, limits)
}
