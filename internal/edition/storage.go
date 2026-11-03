package edition

// StoragePolicy is the site and storage policy (legacy routes, owner interpretation, allowances).
type StoragePolicy struct{}

func storageFor(k Kind) StoragePolicy {
	return StoragePolicy{}
}
