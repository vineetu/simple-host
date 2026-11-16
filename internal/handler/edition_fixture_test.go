package handler

import (
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
)

func groupTestEdition() *edition.Edition { return groupTestEditionWithLimits(config.DefaultLimits()) }
func groupTestEditionWithLimits(limits config.Limits) *edition.Edition {
	return edition.FromPolicies(edition.RoutePolicy{HostedEvents: true}, edition.PresentationPolicy{ProductName: "Test product", ProductAssets: true, CanonicalBaseURL: "https://product.example", StylePath: "product.css", StyleClass: "product"}, edition.IdentityPolicy{RandomHandles: true, HoldingAccounts: true, TeamKeys: true, PaperMetrics: true}, edition.StoragePolicy{EventSites: true, StrictArchives: true, JudgePreviewFrames: true, EventVersionRetention: true, EventOwners: true, CombinedPool: true}, edition.NotificationPolicy{EventScoped: true}, limits)
}

func platformTestEdition() *edition.Edition { return edition.Default(config.DefaultLimits()) }
