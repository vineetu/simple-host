package edition

// IdentityPolicy defines account and credential behavior for an instance.
type IdentityPolicy struct {
	// RandomHandles assigns random handles instead of allowing people to choose them.
	RandomHandles bool
	// PersonalSites permits accounts to own personal sites.
	PersonalSites bool
	// PaperMetrics enables product account and site metrics.
	PaperMetrics bool
}
