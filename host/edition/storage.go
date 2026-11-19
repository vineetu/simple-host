package edition

// StoragePolicy is the site and storage policy (owner interpretation, allowances).
type StoragePolicy struct {
	// Sites.
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

	// Storage.
	// EventOwners interprets owners through event and team membership.
	EventOwners bool
	// CombinedPool combines data and file storage allowances.
	CombinedPool bool
	// FileCaps applies per-site and account deployment caps.
	FileCaps bool
}
