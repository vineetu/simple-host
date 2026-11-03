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
}

func storageFor(k Kind) StoragePolicy {
	p := StoragePolicy{}
	// Sites.
	p.Photos = true
	if k == Hack {
		p.EventSites = true
		p.StrictArchives = true
		p.JudgePreviewFrames = true
		p.EventVersionRetention = true
		p.Photos = false
	}
	return p
}
