package sites

import (
	"context"
	"log"
	"strings"

	"github.com/vsriram/simple-host/host/storage"
)

// RequestSiteCert asks the issuer for *.<handle>.<SITE_DOMAIN> unless it is
// ready. Nothing is requested for flat addresses (TEAM_SITE_FLAT_HOSTS, a
// split base): they live under the base's own wildcard. Under a split base
// a request left from before is withdrawn, so a late certificate cannot
// move a flat person back to two labels.
func (h *Hosts) RequestSiteCert(ctx context.Context, handle string) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if h.addr.TeamSiteFlatHostsOn() || !h.addr.SiteHostsOn() || h.addr.SiteCertDir == "" || !HandleAddressable(handle) {
		return
	}
	if h.addr.SiteCertReadyOn(handle, h.addr.SiteDomain) {
		return
	}
	mode := storage.CertRequestCreate
	if h.addr.FlatFor(handle) {
		mode = storage.CertRequestRemove
	}
	created, err := storage.UpdateCertRequest(storage.CertRequest{Directory: h.addr.SiteCertDir, Key: handle, Mode: mode})
	if err != nil {
		ReportWorkerFailure(ctx, "write certificate request", err)
		return
	}
	if created {
		ReportWorkerProgress(ctx)
		log.Printf("site certs: requested *.%s", h.addr.PersonHostOn(handle, h.addr.SiteDomain))
	}
}
