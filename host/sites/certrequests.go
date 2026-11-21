package sites

import (
	"context"
	"log"
	"strings"

	"github.com/vsriram/simple-host/host/storage"
)

// FamilyCertRequestInput is the family issuer's validated hand-off state.
type FamilyCertRequestInput struct {
	Directory, Suffix, Body      string
	ValidKey, Verified, Wildcard bool
	CertName                     string
}

// SyncFamilyCertRequest updates or removes the family issuer hand-off.
func (s *Service) SyncFamilyCertRequest(ctx context.Context, in FamilyCertRequestInput) {
	if in.Directory == "" || !in.ValidKey {
		return
	}
	if !in.Verified || in.CertName == "" || !in.Wildcard {
		s.RemoveFamilyCertRequest(ctx, in)
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: in.Directory, Key: in.Suffix, Body: in.Body}); err != nil {
		ReportWorkerFailure(ctx, "write family certificate request", err)
	}
}

// RemoveFamilyCertRequest removes the family issuer hand-off if present.
func (s *Service) RemoveFamilyCertRequest(ctx context.Context, in FamilyCertRequestInput) {
	if in.Directory == "" || !in.ValidKey {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: in.Directory, Key: in.Suffix, Mode: storage.CertRequestRemove}); err != nil {
		ReportWorkerFailure(ctx, "remove family certificate request", err)
	}
}

// RequestSiteCert asks the issuer for *.<handle>.<base> under every base a
// person's certificate is requested under, unless it is ready. Under
// TEAM_SITE_FLAT_HOSTS team sites live under the instance's own wildcard,
// so nothing is requested.
func (h *Hosts) RequestSiteCert(ctx context.Context, handle string) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if h.addr.TeamSiteFlatHostsOn() || !h.addr.SiteHostsOn() || !HandleAddressable(handle) {
		return
	}
	for _, base := range h.CertRequestBases() {
		if ctx.Err() != nil {
			return
		}
		if h.addr.SiteCertReadyOn(handle, base) {
			continue
		}
		created, err := storage.UpdateCertRequest(storage.CertRequest{Directory: h.addr.CertDirFor(base), Key: handle, Mode: storage.CertRequestCreate})
		if err != nil {
			ReportWorkerFailure(ctx, "write certificate request", err)
			continue
		}
		if created {
			ReportWorkerProgress(ctx)
			log.Printf("site certs: requested *.%s", h.addr.PersonHostOn(handle, base))
		}
	}
}

// CertRequestBases are the bases a person's certificate is requested under:
// SITE_DOMAIN until the base is handed out (its certificates then only
// renew), and the base from serve on.
func (h *Hosts) CertRequestBases() []string {
	var out []string
	if h.addr.SiteCertDir != "" && !(h.addr.BaseSplit() && h.addr.BaseMove >= BaseMoveCanonical) {
		out = append(out, strings.ToLower(h.addr.SiteDomain))
	}
	if h.addr.BaseSplit() && h.addr.BaseCertDir != "" {
		out = append(out, h.addr.SiteBase)
	}
	return out
}
