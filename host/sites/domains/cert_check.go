package domains

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
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

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// DomainCertProgress reports the certificate of a domain that resolves here
// and whose ownership record matched, but that does not serve HTTPS yet, and
// asks the issuer for one if nobody has: live (the issuer says it is served),
// failed (with the issuer's reason, or the account's daily cap) or issuing.
func (h Handler) DomainCertProgress(ctx context.Context, d model.BoundDomain) (status, reason string) {
	if h.Hosts.DomainCertDir() == "" {
		return "issuing", ""
	}
	if fileExists(h.Hosts.DomainCertFile("ready", d.Domain)) {
		return "live", ""
	}
	// Keep the request (the issuer retries once its wait is over), unless
	// the account is over its daily cap of new certificates.
	asked := h.requestDomainCert(ctx, d)
	if p := h.Hosts.DomainCertFile("failed", d.Domain); fileExists(p) {
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
		return "failed", fmt.Sprintf("this account has asked for %d new domain certificates in the last day; this one is asked for automatically once that day is over", h.Limits.DomainCertsDaily)
	}
	return "issuing", ""
}

// requestDomainCert writes DOMAIN_CERT_DIR/requests/<domain> (or brings an
// existing one up to date). A request for a domain the account has not asked
// for in the last day counts toward its daily cap; false when the cap is
// reached and nothing was written.
func (h Handler) requestDomainCert(ctx context.Context, d model.BoundDomain) bool {
	return h.domainCertSvc().RequestDomainCert(ctx, DomainCertInput{Path: h.Hosts.DomainCertFile("requests", d.Domain), Domain: d.Domain, SiteID: d.SiteID, UserID: d.UserID, Name: d.Name, Token: d.Token})
}

func (h Handler) domainCertSvc() Service {
	return Service{Service: sites.New(h.Database, nil, nil, 0, sites.Deps{Limits: h.Limits})}
}
