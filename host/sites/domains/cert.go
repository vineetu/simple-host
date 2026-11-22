package domains

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/storage"
)

// DomainCertRequestBody is what a request file holds: the site's ownership
// token (the issuer checks the TXT record against it), the domain link's
// target (the issuer acts only while the link still points at this site) and
// the domain's www / bare partner, if it may have one (partner, from
// RequestablePartner).
func DomainCertRequestBody(d model.BoundDomain, partner string) string {
	body := d.Token + "\n" + filepath.Join("..", "by-id", d.UserID, d.Name) + "\n"
	// Line 3: the www / bare partner, put on the same certificate as a
	// redirect when it passes the issuer's checks (domainpartner.go).
	if partner != "" {
		body += partner + "\n"
	}
	return body
}

// DomainCertInput contains a validated hand-off path and the site's identity.
type DomainCertInput struct{ Path, Domain, SiteID, UserID, Name, Token string }

// RequestDomainCert refreshes the issuer request, charging only missing files.
func (s Service) RequestDomainCert(ctx context.Context, in DomainCertInput) bool {
	p := in.Path
	if p == "" || in.Token == "" || in.UserID == "" || in.Name == "" {
		return false
	}
	want := DomainCertRequestBody(model.BoundDomain{Token: in.Token, UserID: in.UserID, Name: in.Name}, s.RequestablePartner(ctx, in.Domain, in.SiteID))
	cur, err := os.ReadFile(p)
	switch {
	case err == nil && string(cur) == want:
		return true
	case err == nil:
	// Out of date (the site was renamed): rewrite, not a new request.
	case errors.Is(err, os.ErrNotExist):
		ok, err := s.AllowDomainCertRequest(ctx, in.UserID, in.Domain, s.Deps.Limits.DomainCertsDaily)
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
func (s Service) CancelDomainCert(_ context.Context, in DomainCertInput) {
	if in.Path == "" {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: filepath.Dir(filepath.Dir(in.Path)), Key: filepath.Base(in.Path), Mode: storage.CertRequestRemove}); err != nil {
		log.Printf("domain certs: withdraw %s: %v", in.Domain, err)
	}
}

// RequestablePartner is the partner to put on the domain's certificate
// request, "" when it has none or another site holds it.
func (s Service) RequestablePartner(ctx context.Context, domain, siteID string) string {
	p := sites.DomainPartner(domain)
	if s.PartnerBoundElsewhere(ctx, p, siteID) {
		return ""
	}
	return p
}
