package handler

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Custom-domain certificates, issued without the operator.
//
// Same hand-off as the per-person site-host certificates (sitehost.go), with
// a root-owned issuer outside this process (deploy/domain-certs/). Once a
// bound custom domain resolves to this server, the domain check drops an
// empty DOMAIN_CERT_DIR/requests/<domain>; the issuer runs certbot HTTP-01
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
// but does not serve HTTPS yet, and asks the issuer for one if nobody has:
// live (the issuer says it is served), failed (with the issuer's reason) or
// issuing.
func (h *SiteHandler) domainCertProgress(domain string) (status, reason string) {
	if h.domainCertDir == "" {
		return "issuing", ""
	}
	if fileExists(h.domainCertFile("ready", domain)) {
		return "live", ""
	}
	if p := h.domainCertFile("failed", domain); fileExists(p) {
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
		// Keep the request so the issuer retries once its wait is over.
		h.requestDomainCert(domain)
		return "failed", why
	}
	h.requestDomainCert(domain)
	return "issuing", ""
}

// requestDomainCert creates DOMAIN_CERT_DIR/requests/<domain> (empty; the
// name is the request). Idempotent.
func (h *SiteHandler) requestDomainCert(domain string) {
	p := h.domainCertFile("requests", domain)
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			log.Printf("domain certs: request %s: %v", domain, err)
		}
		return
	}
	_ = f.Close()
	log.Printf("domain certs: requested %s", domain)
}

// cancelDomainCert withdraws a request that is no longer wanted (the domain
// serves already, or no longer resolves here). Idempotent.
func (h *SiteHandler) cancelDomainCert(domain string) {
	p := h.domainCertFile("requests", domain)
	if p == "" {
		return
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("domain certs: withdraw %s: %v", domain, err)
	}
}
