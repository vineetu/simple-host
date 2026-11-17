package model

import (
	"database/sql"
	"errors"
	"time"
)

// BoundDomain is one bound custom domain and the status it currently reports.
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

// DomainCheck is what SetDomainStatus reports back about a verified domain
// that is failing: since when, and whether its owner has been told.
type DomainCheck struct {
	UserID       string
	Name         string
	FailingSince sql.NullTime
	Notified     bool
	// ReleaseAt is the disconnect date the owner's email gave (only while
	// Notified, and NULL for emails sent before it was stored).
	ReleaseAt sql.NullTime
	// PreviousStatus is the status the row held before this write, read
	// under the row lock: the one a change notice compares against.
	PreviousStatus string
}

// RetiredName is a released <name>.<SITE_DOMAIN>: the site it belongs to, or
// SiteID "" when that site was deleted.
type RetiredName struct {
	SiteID string
}

// ErrDomainTaken means another site has already proved this domain.
var ErrDomainTaken = errors.New("domain is connected to another site")

// BoundDomainOf is the check view of a site's binding.
func BoundDomainOf(info SiteDomainInfo) BoundDomain {
	return BoundDomain{SiteID: info.SiteID, Domain: info.Domain, Status: info.Status, Verified: info.VerifiedAt.Valid,
		PreviousDomain: info.PreviousDomain, UserID: info.UserID, Name: info.Name, Token: info.Token, ProofExempt: info.ProofExempt}
}

// AsPrevious is the view of the site's earlier proven address: that address
// as its domain, active and verified.
func (info SiteDomainInfo) PreviousDomainView(now time.Time) SiteDomainInfo {
	prev := info
	prev.Domain = info.PreviousDomain
	prev.Status = "active"
	prev.LastError = ""
	prev.CertStatus = "live"
	prev.FailingSince = sql.NullTime{}
	if !prev.VerifiedAt.Valid {
		prev.VerifiedAt = sql.NullTime{Time: now, Valid: true}
	}
	prev.PreviousDomain = ""
	return prev
}

var ErrNameIsAccountAddress = errors.New("that name is an account's address")
