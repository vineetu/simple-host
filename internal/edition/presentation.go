package edition

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
