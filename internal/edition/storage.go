package edition

// StoragePolicy is the site and storage policy (legacy routes, owner interpretation, allowances).
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
	// LegacyRoutes exposes the older saved-data APIs.
	LegacyRoutes bool
	// EventOwners interprets owners through event and team membership.
	EventOwners bool
	// CombinedPool combines data and file storage allowances.
	CombinedPool bool
	// FileCaps applies per-site and account deployment caps.
	FileCaps bool
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
