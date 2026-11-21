package edition

// RoutePolicy describes edition-specific routes.
type RoutePolicy struct {
	// HostedEvents enables hosted event routes.
	HostedEvents bool
}

// PresentationPolicy describes edition-specific presentation.
type PresentationPolicy struct {
	// ProductName is the public product name.
	ProductName string
	// ProductAssets selects product presentation and skills.
	ProductAssets bool
	// CanonicalBaseURL names this product's hosted service. An install at another address is self-hosted.
	CanonicalBaseURL      string
	HostedHostPrefix      string
	StylePath             string
	StyleClass            string
	PluginVersion         string
	ServerCardName        string
	ServerCardDescription string
	SitemapPaths          string
}

// IdentityPolicy defines account and credential behavior for an instance.
type IdentityPolicy struct {
	// RandomHandles assigns random handles instead of allowing people to choose them.
	RandomHandles bool
	// PersonalSites permits accounts to own personal sites.
	PersonalSites bool
	// PaperMetrics enables product account and site metrics.
	PaperMetrics bool
}

// StoragePolicy is the site and storage policy.
type StoragePolicy struct {
	// EventSites uses event and team site addressing and access rules.
	EventSites bool
	// StrictArchives requires an index and includes archive errors in responses.
	StrictArchives bool
	// JudgePreviewFrames permits the judging app to frame previews.
	JudgePreviewFrames bool
	// EventVersionRetention uses event version retention rules.
	EventVersionRetention bool
	// Photos enables the photo tools.
	Photos bool

	// CombinedPool combines data and file storage allowances.
	CombinedPool bool
	// FileCaps applies per-site and account deployment caps.
	FileCaps bool
}

// NotificationPolicy describes notification event and access scope.
type NotificationPolicy struct {
	// EventScoped enables group membership and website scoping.
	EventScoped bool
}
