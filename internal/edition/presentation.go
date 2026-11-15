package edition

// PresentationPolicy describes edition-specific presentation.
type PresentationPolicy struct {
	// ProductName is the public product name.
	ProductName string
	// ProductAssets selects Simple Hack presentation and skills.
	ProductAssets bool
}

func presentationFor(k Kind) PresentationPolicy {
	if k == Hack {
		return PresentationPolicy{ProductName: "Simple Hack", ProductAssets: true}
	}
	return PresentationPolicy{ProductName: "Simple Host"}
}
