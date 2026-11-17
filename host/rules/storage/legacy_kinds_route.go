package storage

// LegacyEntryDestination identifies the handler for an entry mutation.
type LegacyEntryDestination uint8

const (
	// LegacyEntryVisitor is a visitor's own entry mutation.
	LegacyEntryVisitor LegacyEntryDestination = iota
	// LegacyEntryOwner is an owner's private-list mutation.
	LegacyEntryOwner
	// LegacyEntryBoard is a shared-board mutation.
	LegacyEntryBoard
)

// LegacyEntryRoute preserves the different owner and board precedence on delete.
func LegacyEntryRoute(kind string, owner, deleting bool) LegacyEntryDestination {
	if deleting && owner {
		return LegacyEntryOwner
	}
	if kind == "board" {
		return LegacyEntryBoard
	}
	if owner {
		return LegacyEntryOwner
	}
	return LegacyEntryVisitor
}
