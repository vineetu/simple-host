package sites

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/storage"
)

// DomainCertDeps supplies partner eligibility and certificate request quotas.
type DomainCertDeps struct {
	Partner func(context.Context, string, string) string
	Allow   func(context.Context, string, string) (bool, error)
}

// DomainCertInput contains a validated hand-off path and the site's identity.
type DomainCertInput struct{ Path, Domain, SiteID, UserID, Name, Token string }

// RequestDomainCert refreshes the issuer request, charging only missing files.
func (s *Service) RequestDomainCert(ctx context.Context, in DomainCertInput) bool {
	p := in.Path
	if p == "" || in.Token == "" || in.UserID == "" || in.Name == "" {
		return false
	}
	partner := s.deps.DomainCerts.Partner(ctx, in.Domain, in.SiteID)
	want := in.Token + "\n" + filepath.Join("..", "by-id", in.UserID, in.Name) + "\n"
	if partner != "" {
		want += partner + "\n"
	}
	cur, err := os.ReadFile(p)
	switch {
	case err == nil && string(cur) == want:
		return true
	case err == nil:
	// Out of date (the site was renamed): rewrite, not a new request.
	case errors.Is(err, os.ErrNotExist):
		ok, err := s.deps.DomainCerts.Allow(ctx, in.UserID, in.Domain)
		if err != nil {
			log.Printf("domain certs: cap check %s: %v", in.Domain, err)
			return false
		}
		if !ok {
			return false
		}
	default:
		log.Printf("domain certs: read request %s: %v", in.Domain, err)
		return false
	}
	// Written in place: the issuer skips a request it cannot read whole and
	// sees it again on its next run.
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: filepath.Dir(filepath.Dir(p)), Key: filepath.Base(p), Body: want, Mode: storage.CertRequestWrite}); err != nil {
		log.Printf("domain certs: request %s: %v", in.Domain, err)
		return false
	}
	log.Printf("domain certs: requested %s", in.Domain)
	return true
}

// CancelDomainCert withdraws the issuer request if present.
func (s *Service) CancelDomainCert(_ context.Context, in DomainCertInput) {
	if in.Path == "" {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: filepath.Dir(filepath.Dir(in.Path)), Key: filepath.Base(in.Path), Mode: storage.CertRequestRemove}); err != nil {
		log.Printf("domain certs: withdraw %s: %v", in.Domain, err)
	}
}
