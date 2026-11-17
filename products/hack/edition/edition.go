package edition

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

// Kind identifies the product edition.
type Kind int

const (
	// Host is the website hosting edition.
	Host Kind = iota
	// Hack is the hosted events edition.
	Hack
)

// String returns the edition name.
func (k Kind) String() string {
	if k == Hack {
		return "hack"
	}
	return "host"
}

type RoutePolicy = edition.RoutePolicy
type PresentationPolicy = edition.PresentationPolicy
type IdentityPolicy = edition.IdentityPolicy
type StoragePolicy = edition.StoragePolicy
type NotificationPolicy = edition.NotificationPolicy

// ForKind constructs the product's policy tables.
func ForKind(k Kind) *edition.Edition { return ForKindWithLimits(k, config.DefaultLimits()) }
func ForKindWithLimits(k Kind, limits config.Limits) *edition.Edition {
	return edition.FromPolicies(routesFor(k), presentationFor(k), identityFor(k), storageFor(k), notificationsFor(k), limits)
}
func routesFor(k Kind) RoutePolicy {
	return RoutePolicy{HostedEvents: k == Hack}
}

func presentationFor(k Kind) PresentationPolicy {
	if k == Hack {
		return PresentationPolicy{ProductName: "Simple Hack", ProductAssets: true, CanonicalBaseURL: "https://simple-hack.app", HostedHostPrefix: "simple-hack.", StylePath: "hack-ink.css", StyleClass: "sh-hack", PluginVersion: "0.27.20", ServerCardName: "app.simple-hack/mcp", ServerCardDescription: "Run events, join teams, publish team websites, and judge entries through the signed-in connector.", SitemapPaths: "/ /get-started /companies /directory /watch /terms /privacy.html"}
	}
	return PresentationPolicy{ProductName: "Simple Host"}
}

func identityFor(k Kind) IdentityPolicy {
	if k == Hack {
		return IdentityPolicy{RandomHandles: true, HoldingAccounts: true, TeamKeys: true, PaperMetrics: true}
	}
	return IdentityPolicy{PersonalSites: true}
}

func storageFor(k Kind) StoragePolicy {
	p := StoragePolicy{}
	if k == Hack {
		// Sites.
		p.EventSites = true
		p.StrictArchives = true
		p.JudgePreviewFrames = true
		p.EventVersionRetention = true
		// Storage.
		p.EventOwners = true
		p.CombinedPool = true
	} else {
		p.Photos = true
		p.LegacyRoutes = true
		p.FileCaps = true
	}
	return p
}

func notificationsFor(k Kind) NotificationPolicy {
	return NotificationPolicy{EventScoped: k == Hack}
}
