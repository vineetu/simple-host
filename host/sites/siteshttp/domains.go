package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// normalizeDomain trims, lowercases, strips a leading scheme and any path, and
// strips a trailing dot. Rejects empty, non-ASCII, missing-dot, path/space
// residues, our own platform hosts, and malformed labels. Total length ≤253.
func (h *SiteHandler) normalizeDomain(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("domain is required")
	}

	// Strip scheme if present (http://example.com or https://example.com/path).
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip path / query / fragment after first '/'.
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	// Strip port if someone pasted host:port (rare for custom domains).
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	s = strings.ToLower(s)

	if s == "" {
		return "", errors.New("domain is required")
	}
	if err := sites.DomainShape(s); err != nil {
		return "", err
	}

	// Reject hijacking our own zone: exact match or subdomain of siteDomain /
	// contentHost (covers cname.<siteDomain> when CNAME_TARGET uses the default).
	if isOwnHost(s, h.ContentHost) {
		return "", errors.New("cannot bind a platform host as a custom domain")
	}
	for _, b := range h.Hosts.Addressing().KnownBases() {
		if isOwnHost(s, b) {
			return "", errors.New("cannot bind a platform host as a custom domain")
		}
	}
	// The platform's other zones (the public site, event domains such as
	// simple-hack.app).
	for _, z := range h.Hosts.PlatformZones() {
		if isOwnHost(s, z) {
			return "", errors.New("cannot bind a platform host as a custom domain")
		}
	}

	return s, nil
}

// domainCandidate lowercases raw and strips a scheme, path, port and trailing
// dot — the same clean-up normalizeDomain starts with — without validating.
func domainCandidate(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// isOwnHost reports whether host equals base or is a subdomain of base.
func isOwnHost(host, base string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	base = strings.ToLower(strings.TrimSpace(base))
	if host == "" || base == "" {
		return false
	}
	if host == base {
		return true
	}
	return strings.HasSuffix(host, "."+base)
}

// dnsRecord is the DNS record (A or CNAME) the site owner must create.
type dnsRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
}

// dnsRecordFor returns the DNS record a user must add to point their domain at us.
// An apex/registrable domain (e.g. agent-deploy.dev) can't use a CNAME, so it gets
// an A record to the box IP; a subdomain (e.g. recipes.brand.com) gets a CNAME.
func (h *SiteHandler) dnsRecordFor(domain string) dnsRecord {
	if h.CustomDomainIP != "" && web.IsApexDomain(domain) {
		return dnsRecord{Type: "A", Host: domain, Value: h.CustomDomainIP}
	}
	return dnsRecord{
		Type:  "CNAME",
		Host:  domain,
		Value: h.CNAMETarget,
	}
}

// proofRecordFor is the DNS TXT record that proves the domain is the
// owner's: _simple-host.<domain> holding the site's token. nil without a token.
func proofRecordFor(domain, token string) *dnsRecord {
	if token == "" {
		return nil
	}
	return &dnsRecord{Type: "TXT", Host: domainProofHost(domain), Value: token}
}

type domainBindRequest struct {
	Domain string `json:"domain"`
}

type domainResponse struct {
	Domain       any        `json:"domain"` // string or null
	Status       any        `json:"status"` // string or null
	BoundAt      *time.Time `json:"bound_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	TookOverFrom string     `json:"took_over_from,omitempty"`
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	DNS          *dnsRecord `json:"dns,omitempty"`
	// DNSTXT is the ownership record: TXT _simple-host.<domain> = the site's
	// token. Required before a domain is verified or gets a certificate.
	DNSTXT *dnsRecord `json:"dns_txt,omitempty"`
	// CertStatus is the domain's certificate: pending (DNS not pointed here
	// yet), issuing, live or failed (last_error says why; retried).
	CertStatus string `json:"certificate_status,omitempty"`
	// PreviousDomain is the site's earlier address, which keeps serving it
	// until this domain is live and then redirects here.
	PreviousDomain string `json:"previous_domain,omitempty"`
	// FailingSince: a verified domain that stopped passing its checks. After
	// 24 h the owner is emailed; after 72 h it stops being the site's home.
	FailingSince *time.Time `json:"failing_since,omitempty"`
	// PartnerDomain is the domain's www / bare partner (www.brand.com for
	// brand.com, and the reverse): a redirect-only host to the chosen name.
	// DNSPartner is the record it needs (the TXT on the chosen name covers
	// both); PartnerStatus is pending (the chosen name has no certificate
	// yet), live, or not_set_up with PartnerNote saying why.
	PartnerDomain string     `json:"partner_domain,omitempty"`
	DNSPartner    *dnsRecord `json:"dns_partner,omitempty"`
	PartnerStatus string     `json:"partner_status,omitempty"`
	PartnerNote   string     `json:"partner_note,omitempty"`
}

// domainResponseFor is the API view of a site's binding.
func (h *SiteHandler) domainResponseFor(info model.SiteDomainInfo) domainResponse {
	resp := domainResponse{
		Domain:         info.Domain,
		Status:         info.Status,
		LastError:      info.LastError,
		PreviousDomain: info.PreviousDomain,
	}
	if !h.Hosts.IsPlatformSubdomain(info.Domain) {
		rec := h.dnsRecordFor(info.Domain)
		resp.DNS = &rec
		resp.DNSTXT = proofRecordFor(info.Domain, info.Token)
		resp.CertStatus = info.CertStatus
		if resp.CertStatus == "" {
			resp.CertStatus = "pending"
			if info.Status == "active" {
				resp.CertStatus = "live"
			}
		}
		if p := h.PartnerInfoFor(info.Domain, info.SiteID); p != nil {
			resp.PartnerDomain, resp.DNSPartner, resp.PartnerStatus, resp.PartnerNote = p.Domain, p.DNS, p.Status, p.Note
		}
	}
	if info.FailingSince.Valid {
		t := info.FailingSince.Time
		resp.FailingSince = &t
	}
	setDomainTimes(&resp, info, h.Limits.DomainUnprovenTTL, h.Limits.DomainUnprovenMaxAge)
	return resp
}

// bindDomain POST /v1/sites/{sitename}/domain — bind one custom domain (pending DNS).
func (h *SiteHandler) bindDomain(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	site, err := h.SiteForCaller(r, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}

	var req domainBindRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}

	// *.<domain> is an address family for the whole account, not one site's
	// domain (POST /v1/me/address-families).
	if isFamilyRequest(req.Domain) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: req.Domain + " is an address family: every site of the account at <site>." + strings.TrimPrefix(strings.TrimSpace(req.Domain), "*.") + ". Connect it with POST /v1/me/address-families (the connect_domain tool does this for *.<domain>)", Code: "use_address_family"})
		return
	}
	// A free <name>.<SITE_DOMAIN> address: claimed, not proven (we own the zone).
	cand := domainCandidate(req.Domain)
	for _, b := range h.Hosts.ServedBases() {
		if isOwnHost(cand, b) && !strings.EqualFold(cand, b) {
			h.bindPlatformSubdomain(w, r, site, cand)
			return
		}
	}

	domain, err := h.normalizeDomain(req.Domain)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: err.Error(), Code: "invalid_domain"})
		return
	}

	current, has, err := h.opsSitesSvc().GetSiteDomainInfo(r.Context(), site.ID)
	switch {
	case err != nil:
		httpx.WriteInternal(w)
	case has && current.Domain == domain && current.VerifiedAt.Valid:
		httpx.WriteJSON(w, http.StatusOK, h.domainResponseFor(current)) // already its working domain
	case has && current.PreviousDomain == domain:
		h.returnToPreviousDomain(w, r, site, current.Domain)
	default:
		h.connectDomain(w, r, site, domain)
	}
}

// returnToPreviousDomain drops the pending domain of a site going back to
// the earlier address it still serves at.
func (h *SiteHandler) returnToPreviousDomain(w http.ResponseWriter, r *http.Request, site model.Site, pending string) {
	dropped, _, err := h.opsSitesSvc().DropCustomDomain(r.Context(), site.ID, pending)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if dropped != "" {
		if _, err := h.Disk.UnbindDomainOf(dropped, site.UserID, site.Name); err != nil {
			log.Printf("domain: unbind %s: %v", dropped, err)
		}
	}
	h.Hosts.SyncDomainRedirect(r.Context(), site.ID)
	info, _, err := h.opsSitesSvc().GetSiteDomainInfo(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.domainResponseFor(info))
}

// connectDomain binds domain to site (pending DNS): it takes the domain over
// from a site whose binding was never proven, and a pending domain it
// replaces stops pointing at the site.
func (h *SiteHandler) connectDomain(w http.ResponseWriter, r *http.Request, site model.Site, domain string) {
	// The issuer still serves this domain for a binding that just ended:
	// wait until it has let go, so nobody inherits a live server and
	// certificate.
	if fileExists(h.Hosts.DomainCertFile("ready", domain)) {
		if _, err := h.opsSitesSvc().GetSiteByCustomDomain(r.Context(), domain); errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "this domain was just disconnected and is still being released; try again in 10 minutes", Code: "domain_releasing"})
			return
		}
	}
	holder, replaced, err := h.opsSitesSvc().BindCustomDomain(r.Context(), site.ID, domain)
	if errors.Is(err, model.ErrDomainTaken) {
		httpx.WriteJSON(w, http.StatusConflict, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{"domain is connected to another site", "domain_taken"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	tookOverFrom := ""
	if holder != nil {
		h.Hosts.CancelDomainCert(domain)
		h.releaseDomainFiles(*holder)
		tookOverFrom = holder.Handle + "/" + holder.Name
	}
	// A proven replaced domain is kept (previous_domain) and serves until
	// this one is live; a pending one stops pointing at the site.
	if replaced != "" {
		if _, err := h.Disk.UnbindDomainOf(replaced, site.UserID, site.Name); err != nil {
			log.Printf("domain: unbind replaced %s: %v", replaced, err)
		}
		h.Hosts.CancelDomainCert(replaced)
	}
	if err := h.Disk.BindDomain(site.UserID, site.Name, domain); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", domain, site.UserID, site.Name, err)
	}
	h.Hosts.SyncDomainRedirect(r.Context(), site.ID) // follows only a proven domain
	info, _, err := h.opsSitesSvc().GetSiteDomainInfo(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	resp := h.domainResponseFor(info)
	resp.TookOverFrom = tookOverFrom
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// getDomain GET /v1/sites/{sitename}/domain — current binding + DNS hint.
func (h *SiteHandler) getDomain(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	site, err := h.opsSitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}

	info, ok, err := h.opsSitesSvc().GetSiteDomainInfo(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, domainResponse{Domain: nil, Status: nil})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, h.domainResponseFor(info))
}

func setDomainTimes(resp *domainResponse, info model.SiteDomainInfo, ttl, maxAge time.Duration) {
	if info.BoundAt.Valid {
		t := info.BoundAt.Time
		resp.BoundAt = &t
		if !info.VerifiedAt.Valid {
			// DNS not pointed here: 24 hours. Pointed here but never proven
			// (the certificate keeps failing): maxAge.
			expires := t.Add(ttl)
			if info.CertStatus != "" && info.CertStatus != "pending" {
				expires = t.Add(maxAge)
			}
			resp.ExpiresAt = &expires
		}
	}
	if info.VerifiedAt.Valid {
		t := info.VerifiedAt.Time
		resp.VerifiedAt = &t
	}
}

// deleteDomain DELETE /v1/sites/{sitename}/domain — unbind custom domain.
func (h *SiteHandler) deleteDomain(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	site, err := h.SiteForCaller(r, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}

	// Drops the domain shown by GET: a pending one gives the site back the
	// earlier address it still serves at; a claimed <name>.<SITE_DOMAIN> stays
	// with the site as a retired name that redirects to its current address.
	// ?domain= names the address the caller means to drop (what it was shown);
	// if the site's domain changed since, nothing is dropped.
	expected := ""
	if q := strings.TrimSpace(r.URL.Query().Get("domain")); q != "" {
		expected = domainCandidate(q)
	}
	dropped, _, err := h.opsSitesSvc().DropCustomDomain(r.Context(), site.ID, expected)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if expected != "" && dropped == "" {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "the site's domain is not " + expected + " (it changed); nothing was changed", Code: "domain_changed"})
		return
	}
	if dropped != "" {
		if _, err := h.Disk.UnbindDomainOf(dropped, site.UserID, site.Name); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	h.Hosts.SyncDomainRedirect(r.Context(), site.ID)

	w.WriteHeader(http.StatusNoContent)
}

// tlsAsk GET /internal/tls-ask?domain=X — Caddy on-demand TLS gate.
// 200 + "ok" if the domain is a bound custom domain (any status) or a platform
// host (siteDomain / contentHost / subdomain of siteDomain); 403 + "no"
// otherwise. Hosted Hack restricts platform subdomains to real event/team sites.
// Never 500 on a miss (DB errors → 403).
func (h *SiteHandler) tlsAsk(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("domain")

	// Platform allowlist first: normalizeDomain rejects our own zone (so owners
	// can't bind it), but Caddy still needs to issue certs for those names.
	candidate := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
	if i := strings.Index(candidate, "://"); i >= 0 {
		candidate = candidate[i+3:]
	}
	if i := strings.IndexByte(candidate, '/'); i >= 0 {
		candidate = candidate[:i]
	}
	if i := strings.IndexByte(candidate, ':'); i >= 0 {
		candidate = candidate[:i]
	}
	own := isOwnHost(candidate, h.ContentHost)
	for _, b := range h.Hosts.ServedBases() {
		own = own || isOwnHost(candidate, b)
	}
	if own && h.Ed.Storage().EventSites {
		own = h.Hosts.GroupTLSHostExists(r.Context(), candidate)
	}
	if own {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	domain, err := h.normalizeDomain(raw)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("no"))
		return
	}

	_, err = h.siteFacts().GetSiteByCustomDomain(r.Context(), domain)
	if err != nil {
		// sql.ErrNoRows or any DB error → deny (never 500 to Caddy on a miss).
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("no"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// domainRedirect answers GET /internal/domain-redirect/{handle}/{sitename}[/rest]
// for the shared content host: once a site has its own domain, its shared-host
// URL only points there (302, so an unbound domain never stays cached).
func (h *SiteHandler) domainRedirect(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.PathValue("handle"))
	name := strings.TrimSpace(r.PathValue("sitename"))
	user, err := h.siteFacts().GetUserByHandle(r.Context(), handle)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	site, err := h.siteFacts().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	markRestrictedSite(w, site)
	// Only a proven domain (or a live family address): a pending one may not
	// serve anything yet.
	info, ok, err := h.Hosts.SiteOwnAddress(r.Context(), site.ID)
	if err != nil || !ok || info.Domain == "" {
		// Stale marker: the domain is gone or unproven. Clean up and let the
		// next request serve.
		_ = h.Disk.ClearDomainRedirect(user.ID, name)
		http.NotFound(w, r)
		return
	}
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	target := "https://" + info.Domain + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// releaseDomainFiles is best-effort cleanup after a database Release: the
// released domain stops pointing at the site, and the content-host redirect
// follows whatever the site now has (an earlier address it got back, or none).
func (h *SiteHandler) releaseDomainFiles(info model.SiteDomainInfo) {
	if err := h.Disk.UnbindDomain(info.Domain); err != nil {
		log.Printf("domain: unbind %s: %v", info.Domain, err)
	}
	h.Hosts.SyncDomainRedirect(context.Background(), info.SiteID)
}
