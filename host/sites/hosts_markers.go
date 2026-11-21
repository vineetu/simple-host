package sites

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
)

// SetDomainCertDir sets DOMAIN_CERT_DIR, the custom domains' certificate
// hand-off directory.
func (h *Hosts) SetDomainCertDir(dir string) { h.domainCertDir = strings.TrimSpace(dir) }

func (h *Hosts) DomainCertDir() string { return h.domainCertDir }

// DomainCertFile is DOMAIN_CERT_DIR/<kind>/<domain>, or "" when the domain
// cannot be a file name.
func (h *Hosts) DomainCertFile(kind, domain string) string {
	domain = strings.ToLower(domain)
	if h.domainCertDir == "" || domain == "" || strings.ContainsAny(domain, "/\\") || strings.HasPrefix(domain, ".") {
		return ""
	}
	return filepath.Join(h.domainCertDir, kind, domain)
}

// CancelDomainCert withdraws the certificate request of a domain let go.
func (h *Hosts) CancelDomainCert(domain string) {
	path := h.DomainCertFile("requests", domain)
	if path == "" {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: filepath.Dir(filepath.Dir(path)), Key: filepath.Base(path), Mode: storage.CertRequestRemove}); err != nil {
		log.Printf("domain certs: withdraw %s: %v", domain, err)
	}
}

// RemoveFamilyRequest withdraws a family's certificate request.
func (h *Hosts) RemoveFamilyRequest(ctx context.Context, suffix string) {
	if h.familyCertDir == "" || !ValidFamilyKey(suffix) {
		return
	}
	if _, err := storage.UpdateCertRequest(storage.CertRequest{Directory: h.familyCertDir, Key: suffix, Mode: storage.CertRequestRemove}); err != nil {
		ReportWorkerFailure(ctx, "remove family certificate request", err)
	}
}

// SyncDomainRedirect points the content-host redirect marker at the site's
// main address when that is not its own Simple Host address (a proven own
// domain, or a live canonical family address), or removes it, and keeps the
// lives-elsewhere marker the family servers read.
func (h *Hosts) SyncDomainRedirect(ctx context.Context, siteID string) {
	_, userID, name, err := repo.GetSiteOwner(ctx, h.db, siteID)
	if err != nil {
		return
	}
	info, has, err := h.SiteOwnAddress(ctx, siteID)
	if err != nil {
		return
	}
	if has {
		err = h.disk.SetDomainRedirect(userID, name, strings.ToLower(info.Domain))
	} else {
		err = h.disk.ClearDomainRedirect(userID, name)
	}
	if err != nil {
		log.Printf("domain: redirect marker for %s/%s: %v", userID, name, err)
	}
	h.SyncLivesElsewhere(ctx, siteID, userID, name)
}

// SyncLivesElsewhere writes the marker the family servers read: the site
// lives on a domain of its own, so its family addresses redirect there.
func (h *Hosts) SyncLivesElsewhere(ctx context.Context, siteID, userID, name string) {
	domain := ""
	if info, has, err := h.SiteOwnDomain(ctx, siteID); err == nil && has {
		domain = strings.ToLower(info.Domain)
	}
	if err := h.disk.SetLivesElsewhere(userID, name, domain); err != nil {
		log.Printf("address families: lives-elsewhere marker for %s/%s: %v", userID, name, err)
	}
}

// SyncUserRedirects re-points the redirect of every site of an account (its
// main address may have changed).
func (h *Hosts) SyncUserRedirects(ctx context.Context, userID string) {
	list, err := repo.ListSitesByUser(ctx, h.db, userID)
	if err != nil {
		log.Printf("address families: sites of %s: %v", userID, err)
		return
	}
	for _, s := range list {
		h.SyncDomainRedirect(ctx, s.ID)
	}
}

// LockSiteName acquires the per-site upload mutex for one account's site name
// and returns its unlock func. Keyed by account and name: two people's sites
// of the same name never wait on each other.
func LockSiteName(locks *sync.Map, userID, name string) func() {
	mu, _ := locks.LoadOrStore(userID+"/"+name, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
