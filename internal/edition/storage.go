package edition

// StoragePolicy is the site and storage policy (legacy routes, owner interpretation, allowances).
type StoragePolicy struct {
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
	// Storage.
	if k == Hack {
		p.EventOwners = true
		p.CombinedPool = true
	} else {
		p.LegacyRoutes = true
		p.FileCaps = true
	}
	return p
}
