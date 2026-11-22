package sites

import (
	"context"
	"strings"

	"github.com/vsriram/simple-host/host/sites/repo"
	"golang.org/x/net/publicsuffix"
)

// IsApexDomain reports whether domain is a registrable domain itself
// (brand.com, brand.co.uk), not a name under one.
func IsApexDomain(domain string) bool {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(domain)
	return err == nil && etld1 == domain
}

// WithPlatformDomains is a copy of s that treats domains as the instance's
// platform domains (claimed <name>.<domain> addresses).
func (s *Service) WithPlatformDomains(domains []string) *Service {
	c := *s
	c.Deps.PlatformDomains = append([]string(nil), domains...)
	return &c
}

// DomainPartner is the www / bare partner of a custom domain: www.<apex> for
// an apex, the apex for www.<apex>, "" for any other name.
func DomainPartner(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if rest, ok := strings.CutPrefix(domain, "www."); ok && IsApexDomain(rest) {
		return rest
	}
	if IsApexDomain(domain) {
		return "www." + domain
	}
	return ""
}

// PartnerBoundElsewhere reports whether the partner of a site's domain is
// bound to another site (any account's): it is then never asked for, and
// the issuer skips it too (deploy/domain-certs/issue.sh partner_blocked). A
// failed lookup counts as bound.
func (s *Service) PartnerBoundElsewhere(ctx context.Context, partner, siteID string) bool {
	if s.DB == nil || partner == "" {
		return false
	}
	bound, err := repo.DomainBoundToOtherSite(ctx, s.DB, partner, siteID)
	return err != nil || bound
}
