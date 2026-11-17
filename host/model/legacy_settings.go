package model

import "time"

// DataSettings is one data name's declaration. Kind "" = not declared.
type DataSettings struct {
	Name         string
	Kind         string
	Private      bool
	OnePerPerson bool
	Notify       string
	DeclaredAt   *time.Time
	// MadeBeforeKinds is the site's legacy_data flag: an undeclared name
	// there is Shared whatever SAVED_DATA_DEFAULT_KIND says.
	MadeBeforeKinds bool
	// Shared: a name nobody declared takes saves here as before the kinds
	// (set by the handler from MadeBeforeKinds and SAVED_DATA_DEFAULT_KIND).
	Shared bool
}
