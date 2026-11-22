package domains

import (
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
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

type domainBindRequest struct {
	Domain string `json:"domain"`
}

type domainResponse struct {
	Domain       any                   `json:"domain"` // string or null
	Status       any                   `json:"status"` // string or null
	BoundAt      *time.Time            `json:"bound_at,omitempty"`
	ExpiresAt    *time.Time            `json:"expires_at,omitempty"`
	TookOverFrom string                `json:"took_over_from,omitempty"`
	VerifiedAt   *time.Time            `json:"verified_at,omitempty"`
	LastError    string                `json:"last_error,omitempty"`
	DNS          *sitesroute.DNSRecord `json:"dns,omitempty"`
	// DNSTXT is the ownership record: TXT _simple-host.<domain> = the site's
	// token. Required before a domain is verified or gets a certificate.
	DNSTXT *sitesroute.DNSRecord `json:"dns_txt,omitempty"`
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
	PartnerDomain string                `json:"partner_domain,omitempty"`
	DNSPartner    *sitesroute.DNSRecord `json:"dns_partner,omitempty"`
	PartnerStatus string                `json:"partner_status,omitempty"`
	PartnerNote   string                `json:"partner_note,omitempty"`
}

func (h Handler) domainResponseFor(info model.SiteDomainInfo) domainResponse {
	resp := domainResponse{
		Domain:         info.Domain,
		Status:         info.Status,
		LastError:      info.LastError,
		PreviousDomain: info.PreviousDomain,
	}
	if !h.Hosts.IsPlatformSubdomain(info.Domain) {
		rec := h.DNSRecordFor(info.Domain)
		resp.DNS = &rec
		resp.DNSTXT = sitesroute.ProofRecordFor(info.Domain, info.Token)
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

func (h Handler) bindDomain(w http.ResponseWriter, r *http.Request) {
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
			sitesroute.WriteSiteNotFound(w, siteName)
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
	if sitesroute.IsFamilyRequest(req.Domain) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: req.Domain + " is an address family: every site of the account at <site>." + strings.TrimPrefix(strings.TrimSpace(req.Domain), "*.") + ". Connect it with POST /v1/me/address-families (the connect_domain tool does this for *.<domain>)", Code: "use_address_family"})
		return
	}
	// A free <name>.<SITE_DOMAIN> address: claimed, not proven (we own the zone).
	cand := sitesroute.DomainCandidate(req.Domain)
	for _, b := range h.Hosts.ServedBases() {
		if sitesroute.IsOwnHost(cand, b) && !strings.EqualFold(cand, b) {
			h.bindPlatformSubdomain(w, r, site, cand)
			return
		}
	}

	domain, err := h.NormalizeDomain(req.Domain)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: err.Error(), Code: "invalid_domain"})
		return
	}

	info, tookOverFrom, err := h.svc().BindDomain(r.Context(), site, domain)
	var refusal *sites.Error
	switch {
	case errors.Is(err, model.ErrDomainTaken):
		httpx.WriteError(w, http.StatusConflict, "domain_taken", "domain is connected to another site")
	case errors.As(err, &refusal):
		httpx.WriteError(w, httpx.RefusalStatus(refusal.Code, http.StatusConflict), refusal.Code, refusal.Message)
	case err != nil:
		httpx.WriteInternal(w)
	default:
		resp := h.domainResponseFor(info)
		resp.TookOverFrom = tookOverFrom
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}

func (h Handler) getDomain(w http.ResponseWriter, r *http.Request) {
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

	info, ok, err := h.svc().SiteDomain(r.Context(), user.ID, siteName)
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
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

func (h Handler) deleteDomain(w http.ResponseWriter, r *http.Request) {
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
			sitesroute.WriteSiteNotFound(w, siteName)
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
	// An empty ?domain= (after clean-up) names nothing, so it drops nothing;
	// only a request without the parameter drops whatever is connected.
	expected := sitesroute.DomainCandidate(r.URL.Query().Get("domain"))
	if r.URL.Query().Has("domain") && expected == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "domain must name the domain connected to the site; nothing was changed")
		return
	}
	dropped, _, err := h.svc().DropCustomDomain(r.Context(), site.ID, expected)
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

	// ?return=site answers with the site as it is now (its new live_url)
	// instead of an empty 204.
	if r.URL.Query().Get("return") == "site" {
		if site, err = h.SiteForCaller(r, user.ID, siteName); err != nil {
			httpx.WriteInternal(w)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, h.ToSiteResponse(site, ""))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tlsAsk GET /internal/tls-ask?domain=X — Caddy on-demand TLS gate.
// 200 + "ok" if the domain is a bound custom domain (any status) or a platform
// host (siteDomain / contentHost / subdomain of siteDomain); 403 + "no"
// otherwise. Hosted Hack restricts platform subdomains to real event/team sites.
// Never 500 on a miss (DB errors → 403).
func (h Handler) tlsAsk(w http.ResponseWriter, r *http.Request) {
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
	own := sitesroute.IsOwnHost(candidate, h.ContentHost)
	for _, b := range h.Hosts.ServedBases() {
		own = own || sitesroute.IsOwnHost(candidate, b)
	}
	if own && h.Ed.Storage().EventSites {
		own = h.Hosts.GroupTLSHostExists(r.Context(), candidate)
	}
	if own {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	domain, err := h.NormalizeDomain(raw)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("no"))
		return
	}

	_, err = h.SitesSvc().GetSiteByCustomDomain(r.Context(), domain)
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
func (h Handler) domainRedirect(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.PathValue("handle"))
	name := strings.TrimSpace(r.PathValue("sitename"))
	user, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sitesroute.MarkRestrictedSite(w, site)
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

// claimableSubdomain validates a requested <label>.<siteDomain>: one DNS label, letters/digits/hyphens,
// no edge hyphen, not reserved, not a platform host. While the base moves (sitebase.go) a name under
// either domain is accepted, and it is always kept under the base handed out.
func (h Handler) claimableSubdomain(host string) (string, error) {
	label, _, ok := h.Hosts.UserHostLabel(host)
	base := h.Hosts.Addressing().HandoutBase()
	if !ok {
		return "", errors.New("a free address is one name under " + base + ", e.g. my-shop." + base)
	}
	return sites.ClaimPlatformAddress(label, base, h.SiteDomain, h.ContentHost, h.CNAMETarget)
}

// bindPlatformSubdomain claims <label>.<siteDomain> for site: verified at once,
// refused if any other site holds it.
func (h Handler) bindPlatformSubdomain(w http.ResponseWriter, r *http.Request, site model.Site, requested string) {
	host, err := h.claimableSubdomain(requested)
	if err != nil {
		code := "invalid_name"
		if errors.Is(err, sites.ErrSubdomainReserved) {
			code = "name_reserved"
		}
		httpx.WriteError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	released, err := h.svc().ClaimPlatformSubdomain(r.Context(), site.ID, host)
	if err != nil {
		if errors.Is(err, model.ErrNameIsAccountAddress) {
			httpx.WriteError(w, http.StatusConflict, "domain_taken", "that name is someone's own address; pick another name")
			return
		}
		if errors.Is(err, model.ErrDomainTaken) {
			httpx.WriteError(w, http.StatusConflict, "domain_taken", "that address is taken by another site; pick another name")
			return
		}
		httpx.WriteInternal(w)
		return
	}
	// A site has one address of its own; a replaced one stops serving (a claimed name redirects as a retired name).
	for _, d := range released {
		if err := h.Disk.UnbindDomain(d); err != nil {
			log.Printf("domain: unbind previous %s for %s: %v", d, site.Name, err)
		}
	}
	if err := h.Disk.BindDomain(site.UserID, site.Name, host); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", host, site.UserID, site.Name, err)
	}
	h.Hosts.SyncDomainRedirect(r.Context(), site.ID)
	now := time.Now()
	httpx.WriteJSON(w, http.StatusOK, domainResponse{
		Domain:     host,
		Status:     "active",
		BoundAt:    &now,
		VerifiedAt: &now,
	})
}
