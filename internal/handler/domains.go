package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	if isOwnHost(s, h.siteDomain) || isOwnHost(s, h.contentHost) {
		return "", errors.New("cannot bind a platform host as a custom domain")
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

// dnsRecord is the CNAME the site owner must create.
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

type domainBindRequest struct {
	Domain string `json:"domain"`
}

type domainResponse struct {
	Domain     any        `json:"domain"` // string or null
	Status     any        `json:"status"` // string or null
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	DNS        *dnsRecord `json:"dns,omitempty"`
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

	var req domainBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	domain, err := h.normalizeDomain(req.Domain)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if err := db.SetCustomDomain(r.Context(), h.database, site.ID, domain); err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "domain already taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.BindDomain(site.UserID, site.Name, domain); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	rec := h.dnsRecordFor(domain)
	writeJSON(w, http.StatusOK, domainResponse{
		Domain: domain,
		Status: "pending",
		DNS:    &rec,
	})
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

	rec := h.dnsRecordFor(info.Domain)
	resp := domainResponse{
		Domain:    info.Domain,
		Status:    info.Status,
		LastError: info.LastError,
		DNS:       &rec,
	}
	if info.VerifiedAt.Valid {
		t := info.VerifiedAt.Time
		resp.VerifiedAt = &t
	}
	writeJSON(w, http.StatusOK, resp)
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

	info, ok, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ClearCustomDomain(r.Context(), h.database, site.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if ok && info.Domain != "" {
		if err := h.disk.UnbindDomain(info.Domain); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	}

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
	if isOwnHost(candidate, h.siteDomain) || isOwnHost(candidate, h.contentHost) {
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
