package edition

// PresentationPolicy describes edition-specific presentation.
type PresentationPolicy struct {
	// ProductName is the public product name.
	ProductName string
	// HackAssets selects Simple Hack presentation and skills.
	HackAssets bool
}

func presentationFor(k Kind) PresentationPolicy {
	if k == Hack {
		return PresentationPolicy{ProductName: "Simple Hack", HackAssets: true}
	}
	return PresentationPolicy{ProductName: "Simple Host"}
}
