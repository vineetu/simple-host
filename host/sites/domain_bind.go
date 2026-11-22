package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"

	"github.com/vsriram/simple-host/host/model"
)

// BindDomain connects domain (already normalised) to site and returns the
// site's domain state after, with the site it took the domain over from
// ("handle/name", or ""). Its working domain again is a no-op; the earlier
// address it still serves at drops the pending one instead; otherwise the
// domain is bound pending DNS, taken over from a site whose binding was never
// proven. Refusals: model.ErrDomainTaken, or Error code domain_releasing while
// the issuer still serves the domain for a binding that just ended.
func (s *Service) BindDomain(ctx context.Context, site model.Site, domain string) (model.SiteDomainInfo, string, error) {
	current, has, err := s.GetSiteDomainInfo(ctx, site.ID)
	switch {
	case err != nil:
		return model.SiteDomainInfo{}, "", err
	case has && current.Domain == domain && current.VerifiedAt.Valid:
		return current, "", nil
	case has && current.PreviousDomain == domain:
		return s.returnToPreviousDomain(ctx, site, current.Domain)
	}
	return s.connectDomain(ctx, site, domain)
}

// returnToPreviousDomain drops the pending domain of a site going back to
// the earlier address it still serves at.
func (s *Service) returnToPreviousDomain(ctx context.Context, site model.Site, pending string) (model.SiteDomainInfo, string, error) {
	dropped, _, err := s.DropCustomDomain(ctx, site.ID, pending)
	if err != nil {
		return model.SiteDomainInfo{}, "", err
	}
	if dropped != "" {
		if _, err := s.disk.UnbindDomainOf(dropped, site.UserID, site.Name); err != nil {
			log.Printf("domain: unbind %s: %v", dropped, err)
		}
	}
	s.deps.Hosts.SyncDomainRedirect(ctx, site.ID)
	info, _, err := s.GetSiteDomainInfo(ctx, site.ID)
	return info, "", err
}

// connectDomain binds domain to site (pending DNS): it takes the domain over
// from a site whose binding was never proven, and a pending domain it
// replaces stops pointing at the site.
func (s *Service) connectDomain(ctx context.Context, site model.Site, domain string) (model.SiteDomainInfo, string, error) {
	hosts := s.deps.Hosts
	// The issuer still serves this domain for a binding that just ended:
	// wait until it has let go, so nobody inherits a live server and
	// certificate.
	if regularFile(hosts.DomainCertFile("ready", domain)) {
		if _, err := s.GetSiteByCustomDomain(ctx, domain); errors.Is(err, sql.ErrNoRows) {
			return model.SiteDomainInfo{}, "", &Error{Code: "domain_releasing", Message: "this domain was just disconnected and is still being released; try again in 10 minutes"}
		}
	}
	holder, replaced, err := s.BindCustomDomain(ctx, site.ID, domain)
	if err != nil {
		return model.SiteDomainInfo{}, "", err
	}
	tookOverFrom := ""
	if holder != nil {
		hosts.CancelDomainCert(domain)
		s.ReleaseDomainFiles(*holder)
		tookOverFrom = holder.Handle + "/" + holder.Name
	}
	// A proven replaced domain is kept (previous_domain) and serves until
	// this one is live; a pending one stops pointing at the site.
	if replaced != "" {
		if _, err := s.disk.UnbindDomainOf(replaced, site.UserID, site.Name); err != nil {
			log.Printf("domain: unbind replaced %s: %v", replaced, err)
		}
		hosts.CancelDomainCert(replaced)
	}
	if err := s.disk.BindDomain(site.UserID, site.Name, domain); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", domain, site.UserID, site.Name, err)
	}
	hosts.SyncDomainRedirect(ctx, site.ID) // follows only a proven domain
	info, _, err := s.GetSiteDomainInfo(ctx, site.ID)
	return info, tookOverFrom, err
}

// ReleaseDomainFiles is best-effort cleanup after a database release: the
// released domain stops pointing at the site, and the content-host redirect
// follows whatever the site now has (an earlier address it got back, or none).
func (s *Service) ReleaseDomainFiles(info model.SiteDomainInfo) {
	if err := s.disk.UnbindDomain(info.Domain); err != nil {
		log.Printf("domain: unbind %s: %v", info.Domain, err)
	}
	s.deps.Hosts.SyncDomainRedirect(context.Background(), info.SiteID)
}

func regularFile(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// SiteDomain is the domain state of the account's site by name (has false
// when it has none). Error site_not_found when the account has no such site.
func (s *Service) SiteDomain(ctx context.Context, userID, name string) (info model.SiteDomainInfo, has bool, err error) {
	site, err := s.GetSiteByUser(ctx, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return info, false, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return info, false, err
	}
	return s.GetSiteDomainInfo(ctx, site.ID)
}
