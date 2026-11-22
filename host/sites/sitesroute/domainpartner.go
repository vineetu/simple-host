package sitesroute

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/sites"
)

// www and the bare domain.
//
// Connecting brand.com or www.brand.com also sets up the other name, its
// partner, as a redirect-only host (301 to the name chosen). The ownership
// record on the chosen name covers both. The issuer (deploy/domain-certs/)
// puts the partner on the same certificate only when it resolves here and
// nothing else on this server answers it; otherwise the chosen name is served
// alone and its ready marker says why the partner is not set up. Nothing is
// stored for the partner: it follows from the chosen name, and its state is
// the issuer's ready marker.

// partnerOf is the partner of a site's custom domain, or "" when it has none
// (a claimed <name>.<SITE_DOMAIN> never has one).
func (h *Handler) PartnerOf(domain string) string {
	if domain == "" || h.Hosts.IsPlatformSubdomain(domain) {
		return ""
	}
	return sites.DomainPartner(domain)
}

// partnerState reads the partner's state from the issuer's ready marker for
// domain: "live" (on the certificate, redirecting), "not_set_up" with the
// issuer's reason, or "pending" while the chosen name has no certificate yet.
func (h *Handler) PartnerState(domain string) (state, note string, since time.Time) {
	p := h.Hosts.DomainCertFile("ready", domain)
	if p == "" {
		return "pending", "", time.Time{}
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "pending", "", time.Time{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "pending", "", time.Time{}
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "partner "):
		return "live", "", st.ModTime()
	case strings.HasPrefix(line, "partner-not-set-up "):
		_, why, _ := strings.Cut(line, ": ")
		if len(why) > 200 {
			why = why[:200]
		}
		return "not_set_up", why, st.ModTime()
	}
	// Issued before partners existed: asked for again on the next check.
	return "not_set_up", "", st.ModTime()
}

type partnerInfo struct {
	Domain string
	DNS    *DNSRecord
	Status string
	Note   string
}

func (h *Handler) PartnerInfoFor(domain, siteID string) *partnerInfo {
	p := h.PartnerOf(domain)
	// Only where the issuer sets partners up (DOMAIN_CERT_DIR): elsewhere
	// (certificates by hand, Caddy on event boxes) nothing would serve it,
	// so it is not offered. Nor when another site has bound the partner.
	if p == "" || h.Hosts.DomainCertDir() == "" || h.SitesSvc().PartnerBoundElsewhere(context.Background(), p, siteID) {
		return nil
	}
	rec := h.DNSRecordFor(p)
	info := &partnerInfo{Domain: p, DNS: &rec, Status: "pending"}
	info.Status, info.Note, _ = h.PartnerState(domain)
	if info.Status == "not_set_up" && info.Note == "" {
		info.Note = "add the DNS record for " + p + "; it is set up automatically once it points here"
	}
	return info
}
