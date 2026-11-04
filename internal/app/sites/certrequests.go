package sites

import (
	"context"
	"log"
	"strings"

	"github.com/vsriram/simple-host/internal/storage"
)

// CertRequestDeps supplies address and readiness checks for site certificate requests.
type CertRequestDeps struct {
	SiteHostsOn       func() bool
	HandleAddressable func(string) bool
	Bases             func() []string
	Ready             func(string, string) bool
	Directory         func(string) string
	PersonHost        func(string, string) string
}

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

// SiteCertRequestInput identifies the account requesting site certificates.
type SiteCertRequestInput struct{ Handle string }

// RequestSiteCert creates missing markers for each configured certificate base.
func (s *Service) RequestSiteCert(ctx context.Context, in SiteCertRequestInput) {
	handle := strings.ToLower(strings.TrimSpace(in.Handle))
	d := s.deps.CertRequests
	if !d.SiteHostsOn() || !d.HandleAddressable(handle) {
		return
	}
	for _, base := range d.Bases() {
		if ctx.Err() != nil {
			return
		}
		if d.Ready(handle, base) {
			continue
		}
		created, err := storage.UpdateCertRequest(storage.CertRequest{Directory: d.Directory(base), Key: handle, Mode: storage.CertRequestCreate})
		if err != nil {
			ReportWorkerFailure(ctx, "write certificate request", err)
			continue
		}
		if created {
			ReportWorkerProgress(ctx)
			log.Printf("site certs: requested *.%s", d.PersonHost(handle, base))
		}
	}
}
