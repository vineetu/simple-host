package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vsriram/simple-host/internal/app/sites"
	db "github.com/vsriram/simple-host/internal/db"
)

// Custom-domain certificates, issued without the operator.
//
// Same hand-off as the per-person site-host certificates (sitehost.go), with
// a root-owned issuer outside this process (deploy/domain-certs/). Once a
// bound custom domain resolves to this server, the domain check drops an
// DOMAIN_CERT_DIR/requests/<domain> (the site's ownership token and the
// domain link's target); the issuer runs certbot HTTP-01
// (webroot), writes the domain's nginx server from its template, reloads
// nginx and writes DOMAIN_CERT_DIR/ready/<domain>. When it cannot, it writes
// DOMAIN_CERT_DIR/failed/<domain> with one line saying why and retries later.
// The issuer only issues for a domain whose link in the sites' domains/
// directory exists (the binding is live) and removes its own server block
// once that link is gone.
//
// Without DOMAIN_CERT_DIR nothing is requested: the operator issues
// certificates by hand, as before.

// SetDomainCerts sets the custom-domain certificate hand-off directory.
func (h *SiteHandler) SetDomainCerts(dir string) {
	h.domainCertDir = strings.TrimSpace(dir)
}

// domainCertFile is DOMAIN_CERT_DIR/<kind>/<domain>, or "" when the domain
// cannot be a file name (never true for a normalized domain).
func (h *SiteHandler) domainCertFile(kind, domain string) string {
	domain = strings.ToLower(domain)
	if h.domainCertDir == "" || domain == "" || strings.ContainsAny(domain, "/\\") || strings.HasPrefix(domain, ".") {
		return ""
	}
	return filepath.Join(h.domainCertDir, kind, domain)
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// domainCertProgress reports the certificate of a domain that resolves here
// and whose ownership record matched, but that does not serve HTTPS yet, and
// asks the issuer for one if nobody has: live (the issuer says it is served),
// failed (with the issuer's reason, or the account's daily cap) or issuing.
func (h *SiteHandler) domainCertProgress(ctx context.Context, d db.BoundDomain) (status, reason string) {
	if h.domainCertDir == "" {
		return "issuing", ""
	}
	if fileExists(h.domainCertFile("ready", d.Domain)) {
		return "live", ""
	}
	// Keep the request (the issuer retries once its wait is over), unless
	// the account is over its daily cap of new certificates.
	asked := h.requestDomainCert(ctx, d)
	if p := h.domainCertFile("failed", d.Domain); fileExists(p) {
		why := "the certificate could not be issued; it is retried every few hours"
		if b, err := os.ReadFile(p); err == nil {
			line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
			if line = strings.TrimSpace(line); line != "" {
				if len(line) > 200 {
					line = line[:200]
				}
				why = "certificate: " + line + " (retried every few hours)"
			}
		}
		return "failed", why
	}
	if !asked {
		return "failed", fmt.Sprintf("this account has asked for %d new domain certificates in the last day; this one is asked for automatically once that day is over", h.limits.DomainCertsDaily)
	}
	return "issuing", ""
}

// domainCertRequestBody is what a request file holds: the site's ownership
// token (the issuer checks the TXT record against it), the domain link's
// target (the issuer acts only while the link still points at this site) and
// the domain's www / bare partner, if it may have one (partner, from
// requestablePartner).
func domainCertRequestBody(d db.BoundDomain, partner string) string {
	body := d.Token + "\n" + filepath.Join("..", "by-id", d.UserID, d.Name) + "\n"
	// Line 3: the www / bare partner, put on the same certificate as a
	// redirect when it passes the issuer's checks (domainpartner.go).
	if partner != "" {
		body += partner + "\n"
	}
	return body
}

// requestDomainCert writes DOMAIN_CERT_DIR/requests/<domain> (or brings an
// existing one up to date). A request for a domain the account has not asked
// for in the last day counts toward its daily cap; false when the cap is
// reached and nothing was written.
func (h *SiteHandler) requestDomainCert(ctx context.Context, d db.BoundDomain) bool {
	return h.domainCertSvc().RequestDomainCert(ctx, sites.DomainCertInput{Path: h.domainCertFile("requests", d.Domain), Domain: d.Domain, SiteID: d.SiteID, UserID: d.UserID, Name: d.Name, Token: d.Token})
}

// cancelDomainCert withdraws a request that is no longer wanted (the domain
// serves already, no longer resolves here, lost its ownership record, or its
// site was taken down). Idempotent.
func (h *SiteHandler) cancelDomainCert(domain string) {
	h.domainCertSvc().CancelDomainCert(context.Background(), sites.DomainCertInput{Path: h.domainCertFile("requests", domain), Domain: domain})
}
