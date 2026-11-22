package domains

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// partnerRetryAfter is how long a partner that could not be set up waits
// before the check asks the issuer again (the issuer's own retry wait).
const partnerRetryAfter = 6 * time.Hour

// wantPartnerCert: a live domain whose partner is not on its certificate yet
// asks the issuer again once the partner resolves here, at most every
// partnerRetryAfter (and keeps a request the issuer has not taken yet).
func (h Handler) wantPartnerCert(ctx context.Context, d model.BoundDomain, ours map[string]bool) bool {
	p := h.PartnerOf(d.Domain)
	if p == "" || h.Hosts.DomainCertDir() == "" || h.SitesSvc().PartnerBoundElsewhere(ctx, p, d.SiteID) {
		return false
	}
	state, _, since := h.PartnerState(d.Domain)
	if state != "not_set_up" {
		return false
	}
	if fileExists(h.Hosts.DomainCertFile("requests", d.Domain)) {
		return true
	}
	if time.Since(since) < partnerRetryAfter {
		return false
	}
	ips, err := sites.LookupHost(ctx, p)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ours[ip] && notify.PublicIP(ip) {
			return true
		}
	}
	return false
}
