package families

import (
	"context"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/storage"
)

// FamilyCertRequestInput is the family issuer's validated hand-off state.
type FamilyCertRequestInput struct {
	Directory, Suffix, Body      string
	ValidKey, Verified, Wildcard bool
	CertName                     string
}

// SyncFamilyCertRequest updates or removes the family issuer hand-off.
func (s Service) SyncFamilyCertRequest(ctx context.Context, in FamilyCertRequestInput) {
	if in.Directory == "" || !in.ValidKey {
		return
	}
	if !in.Verified || in.CertName == "" || !in.Wildcard {
		s.RemoveFamilyCertRequest(ctx, in)
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: in.Directory, Key: in.Suffix, Body: in.Body}); err != nil {
		sites.ReportWorkerFailure(ctx, "write family certificate request", err)
	}
}

// RemoveFamilyCertRequest removes the family issuer hand-off if present.
func (s Service) RemoveFamilyCertRequest(ctx context.Context, in FamilyCertRequestInput) {
	if in.Directory == "" || !in.ValidKey {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: in.Directory, Key: in.Suffix, Mode: storage.CertRequestRemove}); err != nil {
		sites.ReportWorkerFailure(ctx, "remove family certificate request", err)
	}
}
