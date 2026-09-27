package handler

import (
	"context"
	"os"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// www and the bare domain (completeness plan, 2026-09-27).
//
// Connecting brand.com or www.brand.com also sets up the other name, its
// partner, as a redirect-only host (301 to the name chosen). The ownership
// record on the chosen name covers both. The issuer (deploy/domain-certs/)
// puts the partner on the same certificate only when it resolves here and
// nothing else on this server answers it; otherwise the chosen name is served
// alone and its ready marker says why the partner is not set up. Nothing is
// stored for the partner: it follows from the chosen name, and its state is
// the issuer's ready marker.

// partnerRetryAfter is how long a partner that could not be set up waits
// before the check asks the issuer again (the issuer's own retry wait).
const partnerRetryAfter = 6 * time.Hour

// domainPartner is the www / bare partner of a custom domain: www.<apex> for
// an apex, the apex for www.<apex>, "" for any other name.
func domainPartner(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if rest, ok := strings.CutPrefix(domain, "www."); ok && isApexDomain(rest) {
		return rest
	}
	if isApexDomain(domain) {
		return "www." + domain
	}
	return ""
}

// partnerOf is the partner of a site's custom domain, or "" when it has none
// (a claimed <name>.<SITE_DOMAIN> never has one).
func (h *SiteHandler) partnerOf(domain string) string {
	if domain == "" || h.isPlatformSubdomainHost(domain) {
		return ""
	}
	return domainPartner(domain)
}

// partnerState reads the partner's state from the issuer's ready marker for
// domain: "live" (on the certificate, redirecting), "not_set_up" with the
// issuer's reason, or "pending" while the chosen name has no certificate yet.
func (h *SiteHandler) partnerState(domain string) (state, note string, since time.Time) {
	p := h.domainCertFile("ready", domain)
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

// partnerInfo is the API view of a domain's partner.
type partnerInfo struct {
	Domain string
	DNS    *dnsRecord
	Status string
	Note   string
}

func (h *SiteHandler) partnerInfoFor(domain string) *partnerInfo {
	p := h.partnerOf(domain)
	// Only where the issuer sets partners up (DOMAIN_CERT_DIR): elsewhere
	// (certificates by hand, Caddy on event boxes) nothing would serve it,
	// so it is not offered.
	if p == "" || h.domainCertDir == "" {
		return nil
	}
	rec := h.dnsRecordFor(p)
	info := &partnerInfo{Domain: p, DNS: &rec, Status: "pending"}
	info.Status, info.Note, _ = h.partnerState(domain)
	if info.Status == "not_set_up" && info.Note == "" {
		info.Note = "add the DNS record for " + p + "; it is set up automatically once it points here"
	}
	return info
}

// wantPartnerCert: a live domain whose partner is not on its certificate yet
// asks the issuer again once the partner resolves here, at most every
// partnerRetryAfter (and keeps a request the issuer has not taken yet).
func (h *SiteHandler) wantPartnerCert(ctx context.Context, d db.BoundDomain, ours map[string]bool) bool {
	p := h.partnerOf(d.Domain)
	if p == "" || h.domainCertDir == "" {
		return false
	}
	state, _, since := h.partnerState(d.Domain)
	if state != "not_set_up" {
		return false
	}
	if fileExists(h.domainCertFile("requests", d.Domain)) {
		return true
	}
	if time.Since(since) < partnerRetryAfter {
		return false
	}
	ips, err := lookupHost(ctx, p)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ours[ip] && isPublicIP(ip) {
			return true
		}
	}
	return false
}
