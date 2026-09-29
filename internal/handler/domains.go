package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"golang.org/x/net/publicsuffix"
)

// labelRE matches a single DNS label (ASCII only): 1–63 of [a-z0-9-].
var labelRE = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

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
	if strings.ContainsAny(s, "/ \t\r\n") {
		return "", errors.New("domain must not contain spaces or path separators")
	}
	if !strings.Contains(s, ".") {
		return "", errors.New("domain must contain a '.'")
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return "", errors.New("domain must be ASCII only (no IDN yet)")
		}
	}
	if len(s) > 253 {
		return "", errors.New("domain too long (max 253 characters)")
	}

	// Reject hijacking our own zone: exact match or subdomain of siteDomain /
	// contentHost (covers cname.<siteDomain> when CNAME_TARGET uses the default).
	if isOwnHost(s, h.contentHost) {
		return "", errors.New("cannot bind a platform host as a custom domain")
	}
	for _, b := range h.knownBases() {
		if isOwnHost(s, b) {
			return "", errors.New("cannot bind a platform host as a custom domain")
		}
	}

	labels := strings.Split(s, ".")
	for _, label := range labels {
		if label == "" {
			return "", errors.New("domain has an empty label")
		}
		if !labelRE.MatchString(label) {
			return "", errors.New("domain labels must match [a-z0-9-]{1,63}")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("domain labels must not start or end with '-'")
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

// isApexDomain reports whether domain is its own registrable apex (eTLD+1),
// i.e. it has no subdomain label (agent-deploy.dev -> true, x.agent-deploy.dev -> false).
func isApexDomain(domain string) bool {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(domain)
	return err == nil && etld1 == domain
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
	if h.customDomainIP != "" && isApexDomain(domain) {
		return dnsRecord{Type: "A", Host: domain, Value: h.customDomainIP}
	}
	return dnsRecord{
		Type:  "CNAME",
		Host:  domain,
		Value: h.cnameTarget,
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
func (h *SiteHandler) domainResponseFor(info db.SiteDomainInfo) domainResponse {
	resp := domainResponse{
		Domain:         info.Domain,
		Status:         info.Status,
		LastError:      info.LastError,
		PreviousDomain: info.PreviousDomain,
	}
	if !h.isPlatformSubdomainHost(info.Domain) {
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
		if p := h.partnerInfoFor(info.Domain, info.SiteID); p != nil {
			resp.PartnerDomain, resp.DNSPartner, resp.PartnerStatus, resp.PartnerNote = p.Domain, p.DNS, p.Status, p.Note
		}
	}
	if info.FailingSince.Valid {
		t := info.FailingSince.Time
		resp.FailingSince = &t
	}
	setDomainTimes(&resp, info)
	return resp
}

// bindDomain POST /v1/sites/{sitename}/domain — bind one custom domain (pending DNS).
func (h *SiteHandler) bindDomain(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}

	var req domainBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	// A free <name>.<SITE_DOMAIN> address: claimed, not proven (we own the zone).
	cand := domainCandidate(req.Domain)
	for _, b := range h.servedBases() {
		if isOwnHost(cand, b) && !strings.EqualFold(cand, b) {
			h.bindPlatformSubdomain(w, r, site, cand)
			return
		}
	}

	domain, err := h.normalizeDomain(req.Domain)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_domain"})
		return
	}

	current, has, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Already this site's working domain: nothing to redo.
	if has && current.Domain == domain && current.VerifiedAt.Valid {
		writeJSON(w, http.StatusOK, h.domainResponseFor(current))
		return
	}
	// Back to the earlier address the site still serves at: drop the pending one.
	if has && current.PreviousDomain == domain {
		dropped, _, err := db.DropCustomDomain(r.Context(), h.database, site.ID, current.Domain)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if dropped != "" {
			if _, err := h.disk.UnbindDomainOf(dropped, site.UserID, site.Name); err != nil {
				log.Printf("domain: unbind %s: %v", dropped, err)
			}
		}
		h.syncDomainRedirect(r.Context(), site.ID)
		info, _, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, h.domainResponseFor(info))
		return
	}

	// The issuer still serves this domain for a binding that just ended
	// (disconnected or lapsed): wait until it has let go, so nobody inherits
	// a live server and certificate.
	if fileExists(h.domainCertFile("ready", domain)) {
		if _, err := db.GetSiteByCustomDomain(r.Context(), h.database, domain); errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this domain was just disconnected and is still being released; try again in 10 minutes", Code: "domain_releasing"})
			return
		}
	}
	holder, replaced, err := db.BindCustomDomain(r.Context(), h.database, site.ID, domain)
	if err != nil {
		if errors.Is(err, db.ErrDomainTaken) {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{"domain is connected to another site", "domain_taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	tookOverFrom := ""
	if holder != nil {
		h.cancelDomainCert(domain)
		h.releaseDomainFiles(*holder)
		tookOverFrom = holder.Handle + "/" + holder.Name
	}
	// A pending domain this one replaces stops pointing at the site (as read
	// under the bind's lock, not the earlier read). A proven one is kept
	// (previous_domain) and keeps serving until this one is live.
	if replaced != "" {
		if _, err := h.disk.UnbindDomainOf(replaced, site.UserID, site.Name); err != nil {
			log.Printf("domain: unbind replaced %s: %v", replaced, err)
		}
		h.cancelDomainCert(replaced)
	}
	if err := h.disk.BindDomain(site.UserID, site.Name, domain); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", domain, site.UserID, site.Name, err)
	}
	// The content-host redirect follows only a proven domain.
	h.syncDomainRedirect(r.Context(), site.ID)

	info, _, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := h.domainResponseFor(info)
	resp.TookOverFrom = tookOverFrom
	writeJSON(w, http.StatusOK, resp)
}

// getDomain GET /v1/sites/{sitename}/domain — current binding + DNS hint.
func (h *SiteHandler) getDomain(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	info, ok, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, domainResponse{Domain: nil, Status: nil})
		return
	}

	writeJSON(w, http.StatusOK, h.domainResponseFor(info))
}

func setDomainTimes(resp *domainResponse, info db.SiteDomainInfo) {
	if info.BoundAt.Valid {
		t := info.BoundAt.Time
		resp.BoundAt = &t
		if !info.VerifiedAt.Valid {
			// DNS not pointed here: 24 hours. Pointed here but never proven
			// (the certificate keeps failing): db.UnprovenDomainMaxAge().
			expires := t.Add(db.UnprovenDomainTTL())
			if info.CertStatus != "" && info.CertStatus != "pending" {
				expires = t.Add(db.UnprovenDomainMaxAge())
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
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
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
	dropped, _, err := db.DropCustomDomain(r.Context(), h.database, site.ID, expected)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if expected != "" && dropped == "" {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the site's domain is not " + expected + " (it changed); nothing was changed", Code: "domain_changed"})
		return
	}
	if dropped != "" {
		if _, err := h.disk.UnbindDomainOf(dropped, site.UserID, site.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	}
	h.syncDomainRedirect(r.Context(), site.ID)

	w.WriteHeader(http.StatusNoContent)
}

// tlsAsk GET /internal/tls-ask?domain=X — Caddy on-demand TLS gate.
// 200 + "ok" if the domain is a bound custom domain (any status) or a platform
// host (siteDomain / contentHost / subdomain of siteDomain); 403 + "no"
// otherwise. Never 500 on a miss (DB errors → 403).
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
	own := isOwnHost(candidate, h.contentHost)
	for _, b := range h.servedBases() {
		own = own || isOwnHost(candidate, b)
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

	_, err = db.GetSiteByCustomDomain(r.Context(), h.database, domain)
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
	user, err := db.GetUserByHandle(r.Context(), h.database, handle)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Only a proven domain: a pending one may not serve anything yet.
	info, ok, err := h.siteOwnDomain(r.Context(), site.ID)
	if err != nil || !ok || info.Domain == "" {
		// Stale marker: the domain is gone or unproven. Clean up and let the
		// next request serve.
		_ = h.disk.ClearDomainRedirect(user.ID, name)
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

// releaseDomainFiles is best-effort cleanup after a database release: the
// released domain stops pointing at the site, and the content-host redirect
// follows whatever the site now has (an earlier address it got back, or none).
func (h *SiteHandler) releaseDomainFiles(info db.SiteDomainInfo) {
	if err := h.disk.UnbindDomain(info.Domain); err != nil {
		log.Printf("domain: unbind %s: %v", info.Domain, err)
	}
	h.syncDomainRedirect(context.Background(), info.SiteID)
}
