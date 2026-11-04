package model

type BoundDomain struct {
	SiteID         string
	Domain         string
	Status         string
	Verified       bool
	PreviousDomain string
	UserID         string
	Name           string
	// Token is the site's ownership token (TXT _simple-host.<domain>).
	Token string
	// ProofExempt: verified before the TXT proof existed (see SiteDomainInfo).
	ProofExempt bool
	// Suspended: the site or its owner's account is taken down; it gets no
	// checks and no certificate.
	Suspended bool
}
