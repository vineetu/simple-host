package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"golang.org/x/net/publicsuffix"
)

// The address-family API (familyhost.go has the model):
//
//	POST   /v1/me/address-families               connect *.<suffix>
//	GET    /v1/me/address-families               the account's families
//	GET    /v1/me/address-families/{suffix}      one (?sites=1: every site's address)
//	PATCH  /v1/me/address-families/{suffix}      site_prefix, rank, canonical
//	DELETE /v1/me/address-families/{suffix}      disconnect
//	POST   /v1/me/address-families/{suffix}/check  "Check again"
//
// and for the admin: the list, connecting one for an account, the
// certificate (the operator's wildcard lineage), the proof exemption and
// disconnecting.

// SetPlatformZones names more zones that are the platform's own and never an
// owner's domain or family: the public site's host and the event domains
// (e.g. simple-hack.app).
func (h *SiteHandler) SetPlatformZones(zones ...string) {
	h.platformZones = nil
	for _, z := range zones {
		z = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z), "."))
		if z != "" {
			h.platformZones = append(h.platformZones, z)
		}
	}
}

// isPlatformZoneHost: host is (or is under) one of the platform's zones.
func (h *SiteHandler) isPlatformZoneHost(host string) bool {
	if isOwnHost(host, h.contentHost) {
		return true
	}
	for _, b := range h.knownBases() {
		if isOwnHost(host, b) {
			return true
		}
	}
	for _, z := range h.platformZones {
		if isOwnHost(host, z) {
			return true
		}
	}
	if t := strings.ToLower(strings.TrimSpace(h.cnameTarget)); t != "" {
		if reg, err := publicsuffix.EffectiveTLDPlusOne(t); err == nil && isOwnHost(host, reg) {
			return true
		}
	}
	return false
}

// normalizeFamilySuffix cleans and checks the name of a family: "*.brand.com",
// "brand.com" or a pasted URL all mean the suffix brand.com. It must be a
// valid ASCII domain of at least two labels that is not a public suffix
// itself and not the platform's own.
func (h *SiteHandler) normalizeFamilySuffix(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "*.")
	suffix, err := h.normalizeDomain(s)
	if err != nil {
		return "", err
	}
	if h.isPlatformZoneHost(suffix) {
		return "", errors.New("cannot use a platform domain for an address family")
	}
	if reg, err := publicsuffix.EffectiveTLDPlusOne(suffix); err != nil || reg == "" {
		return "", errors.New("an address family needs a domain you own (e.g. *.brand.com), not a public suffix")
	}
	if strings.HasPrefix(suffix, "xn--") || strings.Contains(suffix, ".xn--") {
		return "", errors.New("internationalised domains are not supported yet")
	}
	return suffix, nil
}

// isFamilyRequest: the domain the caller asked for is a wildcard (*.x).
func isFamilyRequest(raw string) bool {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return strings.HasPrefix(s, "*.")
}

// familyCert is a family's certificate as the API shows it.
type familyCert struct {
	// Mode is wildcard (the operator's certificate for *.<suffix>).
	Mode string `json:"mode"`
	// Status: waiting_for_operator (no certificate set up yet), pending
	// (the family is not verified yet), issuing (with the issuer), live, or
	// failed (Note says why).
	Status    string     `json:"status"`
	Note      string     `json:"note,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// familySite is one of the account's sites and its family address.
type familySite struct {
	Site    string `json:"site"`
	Address string `json:"address"`
	// Main: this is the address handed out for the site.
	Main bool `json:"main"`
	// LivesAt: the site lives on a domain of its own; this address redirects there.
	LivesAt string `json:"lives_at,omitempty"`
}

// familyResponse is the API view of a family.
type familyResponse struct {
	ID         string     `json:"id"`
	Family     string     `json:"family"` // *.<suffix>
	Suffix     string     `json:"suffix"`
	SitePrefix string     `json:"site_prefix"`
	Rank       int        `json:"rank"`
	Canonical  bool       `json:"canonical"`
	Status     string     `json:"status"` // pending | active | failing
	Live       bool       `json:"live"`
	LastError  string     `json:"last_error,omitempty"`
	BoundAt    time.Time  `json:"bound_at"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// FailingSince: a verified family that stopped passing its checks.
	FailingSince *time.Time `json:"failing_since,omitempty"`
	ReleaseAt    *time.Time `json:"release_at,omitempty"`
	// DNS is the wildcard record: CNAME *.<suffix> -> the CNAME target, or
	// DNSA the A record alternative.
	DNS         *dnsRecord   `json:"dns,omitempty"`
	DNSA        *dnsRecord   `json:"dns_a,omitempty"`
	DNSTXT      *dnsRecord   `json:"dns_txt,omitempty"`
	ProofExempt bool         `json:"proof_exempt,omitempty"`
	Certificate familyCert   `json:"certificate"`
	ExampleURL  string       `json:"example_url,omitempty"`
	Sites       []familySite `json:"sites,omitempty"`
	// Admin view only.
	Owner    string `json:"owner,omitempty"`
	CertName string `json:"cert_name,omitempty"`
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// familyCertOf reads the family's certificate from the database and the
// issuer's hand-off files.
func (h *SiteHandler) familyCertOf(f db.AddressFamily) familyCert {
	c := familyCert{Mode: f.CertMode}
	switch {
	case f.CertMode != "wildcard":
		c.Status, c.Note = "failed", "certificates per site name are not available yet"
	case f.CertName == "":
		c.Status, c.Note = "waiting_for_operator", "the operator sets up the wildcard certificate for *."+f.Suffix+"; write to "+auth.SupportContact+" if it has been more than a day"
	case !f.VerifiedAt.Valid:
		c.Status = "pending"
	case h.familyIsLive(f):
		c.Status = "live"
		if exp := h.familyReadyValue(f.Suffix, "expires="); exp != "" {
			var n int64
			if _, err := fmt.Sscan(exp, &n); err == nil && n > 0 {
				t := time.Unix(n, 0).UTC()
				c.ExpiresAt = &t
			}
		}
	default:
		c.Status = "issuing"
		if b, err := os.ReadFile(filepath.Join(h.familyCertDir, "failed", f.Suffix)); err == nil && validFamilyKey(f.Suffix) {
			c.Status, c.Note = "failed", strings.TrimSpace(string(b))
		}
	}
	return c
}

// familyReadyValue is one key=value line of the family's ready marker.
func (h *SiteHandler) familyReadyValue(suffix, key string) string {
	if h.familyCertDir == "" || !validFamilyKey(suffix) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(h.familyCertDir, "ready", suffix))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key); ok {
			return v
		}
	}
	return ""
}

func (h *SiteHandler) familyResponseFor(ctx context.Context, f db.AddressFamily, withSites bool) familyResponse {
	resp := familyResponse{
		ID: f.ID, Family: "*." + f.Suffix, Suffix: f.Suffix, SitePrefix: f.SitePrefix, Rank: f.Rank,
		Canonical: f.Canonical, Status: f.Status, Live: h.familyIsLive(f), LastError: f.LastError,
		BoundAt: f.BoundAt, VerifiedAt: timePtr(f.VerifiedAt), FailingSince: timePtr(f.FailingSince),
		ProofExempt: f.ProofExempt, Certificate: h.familyCertOf(f),
	}
	if f.LapseNotifiedAt.Valid {
		resp.ReleaseAt = timePtr(f.ReleaseAt)
	}
	if !f.VerifiedAt.Valid {
		t := f.BoundAt.Add(config.Active().FamilyUnprovenTTL)
		resp.ExpiresAt = &t
	}
	resp.DNS = &dnsRecord{Type: "CNAME", Host: "*." + f.Suffix, Value: h.cnameTarget}
	if h.customDomainIP != "" {
		resp.DNSA = &dnsRecord{Type: "A", Host: "*." + f.Suffix, Value: h.customDomainIP}
	}
	if !f.ProofExempt {
		resp.DNSTXT = &dnsRecord{Type: "TXT", Host: domainProofHost(f.Suffix), Value: f.Token}
	}
	if sites, err := db.ListSitesByUser(ctx, h.database, f.UserID); err == nil {
		for _, s := range sites {
			label, ok := strings.CutPrefix(s.Name, f.SitePrefix)
			if !ok || !familyLabelOK(f, label) {
				continue
			}
			if resp.ExampleURL == "" {
				resp.ExampleURL = "https://" + label + "." + f.Suffix + "/"
			}
			if !withSites {
				break
			}
			fs := familySite{Site: s.Name, Address: "https://" + label + "." + f.Suffix + "/"}
			if own, has, err := h.siteOwnDomain(ctx, s.ID); err == nil && has {
				fs.LivesAt = "https://" + strings.ToLower(own.Domain) + "/"
			} else if main, ok := h.siteFamilyAddress(s.UserID, s.Name); ok && main == label+"."+f.Suffix {
				fs.Main = true
			}
			resp.Sites = append(resp.Sites, fs)
		}
	}
	return resp
}

type familyRequest struct {
	Suffix     string `json:"suffix"`
	Family     string `json:"family"`
	SitePrefix string `json:"site_prefix"`
	Rank       *int   `json:"rank"`
	Canonical  *bool  `json:"canonical"`
	CertMode   string `json:"cert_mode"`
	// Admin only (POST /v1/admin/users/{id}/address-families).
	CertName    string `json:"cert_name"`
	ProofExempt bool   `json:"proof_exempt"`
}

// familiesAvailable writes the refusal when families cannot be used here.
func (h *SiteHandler) familiesAvailable(w http.ResponseWriter) bool {
	if h.familiesOn() {
		return true
	}
	writeJSON(w, http.StatusConflict, errorResponse{Error: "address families are not available on this server", Code: "address_families_unavailable"})
	return false
}

// validRank is the range a family's rank may take.
func validRank(n int) bool { return n >= 0 && n <= 1000 }

// createFamily POST /v1/me/address-families.
func (h *SiteHandler) createFamily(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	h.createFamilyFor(w, r, user.ID, false)
}

// createFamilyFor connects a family for userID (admin: with the operator's
// certificate and the proof exemption).
func (h *SiteHandler) createFamilyFor(w http.ResponseWriter, r *http.Request, userID string, admin bool) {
	if !h.familiesAvailable(w) {
		return
	}
	if u, err := db.GetUserByID(r.Context(), h.database, userID); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "account not found"})
		return
	} else if u.Suspended {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this account is suspended", Code: "account_suspended"})
		return
	}
	var req familyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	raw := req.Suffix
	if raw == "" {
		raw = req.Family
	}
	suffix, err := h.normalizeFamilySuffix(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_domain"})
		return
	}
	switch req.CertMode {
	case "", "wildcard":
	case "per_host":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "certificates per site name (cert_mode per_host) are not available yet: address families use a wildcard certificate that the operator sets up for *." + suffix + " (write to " + auth.SupportContact + ")", Code: "cert_mode_unavailable"})
		return
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cert_mode must be wildcard", Code: "invalid_cert_mode"})
		return
	}
	prefix := strings.ToLower(strings.TrimSpace(req.SitePrefix))
	if !familyPrefixRE.MatchString(prefix) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site_prefix may use lowercase letters, digits and hyphens (not at the start), up to 40 characters", Code: "invalid_site_prefix"})
		return
	}
	rank := 0
	if req.Rank != nil {
		rank = *req.Rank
	}
	if !validRank(rank) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "rank must be between 0 and 1000", Code: "invalid_rank"})
		return
	}
	canonical := true
	if req.Canonical != nil {
		canonical = *req.Canonical
	}
	b := db.FamilyBind{UserID: userID, Suffix: suffix, SitePrefix: prefix, Rank: rank, Canonical: canonical,
		CertMode: "wildcard", Max: config.Active().FamiliesPerAccount}
	if admin {
		name := strings.ToLower(strings.TrimSpace(req.CertName))
		if name != "" && !certNameRE.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cert_name is a certificate lineage name (letters, digits, dots, hyphens)", Code: "invalid_cert_name"})
			return
		}
		b.CertName, b.ProofExempt = name, req.ProofExempt
	}
	f, err := db.BindFamily(r.Context(), h.database, b)
	switch {
	case errors.Is(err, db.ErrFamilyExists):
		f, err = db.GetFamilyByUserSuffix(r.Context(), h.database, userID, suffix)
		if err == nil {
			writeJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
			return
		}
	case errors.Is(err, db.ErrFamilyTaken):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "*." + suffix + " (or a name under it) is connected to another account", Code: "domain_taken"})
		return
	case errors.Is(err, db.ErrFamilyCap):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("an account may connect %d address families on this server", config.Active().FamiliesPerAccount), Code: "address_family_limit"})
		return
	}
	if err != nil {
		log.Printf("address family %s: bind: %v", suffix, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if admin {
		log.Printf("admin %s connected address family *.%s for %s (cert %q, proof exempt %v)", adminName(r), suffix, userID, f.CertName, f.ProofExempt)
	}
	writeJSON(w, http.StatusCreated, h.familyResponseFor(r.Context(), f, false))
}

// certNameRE is a certbot lineage name.
var certNameRE = familyCertNameRE

func adminName(r *http.Request) string {
	if u := auth.GetUser(r.Context()); u != nil {
		return u.Username
	}
	return "?"
}

// listFamilies GET /v1/me/address-families.
func (h *SiteHandler) listFamilies(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	fams, err := db.ListFamiliesByUser(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]familyResponse, 0, len(fams))
	for _, f := range fams {
		out = append(out, h.familyResponseFor(r.Context(), f, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"address_families": out, "available": h.familiesOn()})
}

// callerFamily loads the caller's family named by {suffix} (either form).
func (h *SiteHandler) callerFamily(w http.ResponseWriter, r *http.Request) (db.AddressFamily, bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return db.AddressFamily{}, false
	}
	raw, _ := url.PathUnescape(r.PathValue("suffix"))
	suffix := domainCandidate(strings.TrimPrefix(strings.TrimSpace(raw), "*."))
	f, err := db.GetFamilyByUserSuffix(r.Context(), h.database, user.ID, suffix)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no address family *." + suffix + " on this account", Code: "no_address_family"})
		return db.AddressFamily{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return db.AddressFamily{}, false
	}
	return f, true
}

// getFamily GET /v1/me/address-families/{suffix}.
func (h *SiteHandler) getFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, r.URL.Query().Get("sites") == "1"))
}

type familyPatchRequest struct {
	SitePrefix *string `json:"site_prefix"`
	Rank       *int    `json:"rank"`
	Canonical  *bool   `json:"canonical"`
}

// patchFamily PATCH /v1/me/address-families/{suffix}.
func (h *SiteHandler) patchFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	var req familyPatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.SitePrefix != nil {
		p := strings.ToLower(strings.TrimSpace(*req.SitePrefix))
		if !familyPrefixRE.MatchString(p) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site_prefix may use lowercase letters, digits and hyphens (not at the start), up to 40 characters", Code: "invalid_site_prefix"})
			return
		}
		req.SitePrefix = &p
	}
	if req.Rank != nil && !validRank(*req.Rank) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "rank must be between 0 and 1000", Code: "invalid_rank"})
		return
	}
	f, err := db.UpdateFamily(r.Context(), h.database, f.UserID, f.Suffix, db.FamilyPatch{SitePrefix: req.SitePrefix, Rank: req.Rank, Canonical: req.Canonical})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	h.syncFamilyRequest(f)
	h.refreshFamilies(r.Context())
	writeJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
}

// deleteFamily DELETE /v1/me/address-families/{suffix}.
func (h *SiteHandler) deleteFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	if err := h.releaseFamily(r.Context(), f); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkFamilyNow POST /v1/me/address-families/{suffix}/check.
func (h *SiteHandler) checkFamilyNow(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	if h.familyCheckUserLimiter != nil && !h.familyCheckUserLimiter.allow(f.UserID) {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "checked a moment ago; try again in half a minute (families are also checked every few minutes on their own)", Code: "rate_limited"})
		return
	}
	if h.familiesOn() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*domainProbeTimeout)
		defer cancel()
		h.checkFamily(ctx, f, h.serverAddrs(ctx))
	}
	f, err := db.GetFamilyByID(r.Context(), h.database, f.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "the family was let go", Code: "no_address_family"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
}

// releaseFamily lets a family go: the row, the link (so nginx stops serving
// it at once), the issuer's request (so it withdraws the server; it never
// touches the operator's certificate), and every redirect that followed it.
func (h *SiteHandler) releaseFamily(ctx context.Context, f db.AddressFamily) error {
	if _, err := db.DeleteFamily(ctx, h.database, f.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := h.disk.UnbindFamily(f.Suffix); err != nil {
		log.Printf("address family %s: unbind: %v", f.Suffix, err)
	}
	h.removeFamilyRequest(f.Suffix)
	h.refreshFamilies(ctx)
	h.syncUserRedirects(ctx, f.UserID)
	log.Printf("address family *.%s of %s released", f.Suffix, f.UserID)
	return nil
}

// --- The hand-off with the issuer (deploy/family-certs/issue.sh) ---

// familyRequestBody is requests/<suffix>: the token, the link target, the
// certificate mode and lineage, the site prefix and the reserved labels.
func familyRequestBody(f db.AddressFamily) string {
	return f.Token + "\n" + filepath.Join("..", "by-id", f.UserID) + "\n" + f.CertMode + "\n" + f.CertName + "\n" +
		f.SitePrefix + "\n" + strings.Join(config.Active().ReservedFamilyLabels(), " ") + "\n"
}

// syncFamilyRequest keeps requests/<suffix> in step with a verified family
// that has its certificate set up (rewritten only when it changed, so the
// issuer's path unit fires only then); anything else has none.
func (h *SiteHandler) syncFamilyRequest(f db.AddressFamily) {
	if h.familyCertDir == "" || !validFamilyKey(f.Suffix) {
		return
	}
	if !f.VerifiedAt.Valid || f.CertName == "" || f.CertMode != "wildcard" {
		h.removeFamilyRequest(f.Suffix)
		return
	}
	p := filepath.Join(h.familyCertDir, "requests", f.Suffix)
	body := familyRequestBody(f)
	if cur, err := os.ReadFile(p); err == nil && string(cur) == body {
		return
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		log.Printf("address family %s: request: %v", f.Suffix, err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		log.Printf("address family %s: request: %v", f.Suffix, err)
	}
}

func (h *SiteHandler) removeFamilyRequest(suffix string) {
	if h.familyCertDir == "" || !validFamilyKey(suffix) {
		return
	}
	if err := os.Remove(filepath.Join(h.familyCertDir, "requests", suffix)); err != nil && !os.IsNotExist(err) {
		log.Printf("address family %s: remove request: %v", suffix, err)
	}
}

// --- Checks ---

// checkFamilies is one pass: drop pending families that never proved
// themselves, then check every family that is due.
func (h *SiteHandler) checkFamilies(ctx context.Context) {
	if !h.familiesOn() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, domainPassTimeout)
	defer cancel()
	expired, err := db.ReleaseExpiredFamilies(ctx, h.database, config.Active().FamilyUnprovenTTL)
	if err != nil {
		log.Printf("address families: expiry: %v", err)
	}
	for _, f := range expired {
		h.removeFamilyRequest(f.Suffix)
		log.Printf("address family *.%s: never proved; dropped", f.Suffix)
	}
	due, err := db.ListFamiliesToCheck(ctx, h.database, config.Active().FamilyActiveRecheck)
	if err != nil {
		log.Printf("address families: list: %v", err)
		return
	}
	ours := h.serverAddrs(ctx)
	for _, f := range due {
		h.checkFamily(ctx, f, ours)
	}
	h.refreshFamilies(ctx)
}

// familyProbeLabel is a label nobody has: the wildcard record must answer it.
func familyProbeLabel() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "sh-check-" + hex.EncodeToString(b)
}

// verifyFamilyDNS: the ownership record (unless exempt) and the wildcard:
// a label nobody has resolves here and only here.
func (h *SiteHandler) verifyFamilyDNS(ctx context.Context, f db.AddressFamily, ours map[string]bool) (bool, string) {
	if !f.ProofExempt {
		if ok, why := domainProof(ctx, f.Suffix, f.Token); !ok {
			return false, why
		}
	}
	probe := familyProbeLabel() + "." + f.Suffix
	ips, err := lookupHost(ctx, probe)
	if err != nil || len(ips) == 0 {
		return false, "the wildcard record *." + f.Suffix + " is not seen yet (add it at your registrar: " + h.familyRecordHint(f) + ")"
	}
	here := false
	for _, ip := range ips {
		if !ours[ip] {
			return false, "*." + f.Suffix + " resolves to " + ip + ", which is not this server (remove any other A or AAAA record for *." + f.Suffix + ")"
		}
		if isPublicIP(ip) {
			here = true
		}
	}
	if !here {
		return false, "*." + f.Suffix + " does not resolve to this server yet"
	}
	return true, ""
}

func (h *SiteHandler) familyRecordHint(f db.AddressFamily) string {
	if h.cnameTarget != "" {
		return "CNAME *." + f.Suffix + " -> " + h.cnameTarget
	}
	return "A *." + f.Suffix + " -> " + h.customDomainIP
}

// probeFamily: one of the account's sites answers over HTTPS at its family
// address, through this server (any HTTP answer counts: a site may be
// offline or locked).
func (h *SiteHandler) probeFamily(ctx context.Context, f db.AddressFamily, ours map[string]bool) (bool, string) {
	sites, err := db.ListSitesByUser(ctx, h.database, f.UserID)
	if err != nil {
		return true, ""
	}
	for _, s := range sites {
		label, ok := strings.CutPrefix(s.Name, f.SitePrefix)
		if !ok || !familyLabelOK(f, label) {
			continue
		}
		host := label + "." + f.Suffix
		ips, err := lookupHost(ctx, host)
		if err != nil {
			return false, host + " does not resolve"
		}
		ip := ""
		for _, a := range ips {
			if ours[a] && isPublicIP(a) {
				ip = a
				break
			}
		}
		if ip == "" {
			return false, host + " does not resolve to this server"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
		if err != nil {
			return false, err.Error()
		}
		resp, err := domainProbeVia(ip).Do(req)
		if err != nil {
			return false, "https://" + host + "/ does not answer over HTTPS"
		}
		resp.Body.Close()
		return true, ""
	}
	return true, "" // no site to probe yet
}

// checkFamily proves one family and acts on it: a pending one that passes
// is verified (the first account to prove a name wins it), a verified one
// that keeps failing is warned about and then let go.
func (h *SiteHandler) checkFamily(ctx context.Context, f db.AddressFamily, ours map[string]bool) {
	ok, why := h.verifyFamilyDNS(ctx, f, ours)
	if ok && f.VerifiedAt.Valid && h.familyIsLive(f) {
		ok, why = h.probeFamily(ctx, f, ours)
	}
	if !f.VerifiedAt.Valid {
		if !ok {
			if _, err := db.SetFamilyCheck(ctx, h.database, f.ID, false, why); err != nil {
				log.Printf("address family %s: %v", f.Suffix, err)
			}
			return
		}
		won, losers, err := db.VerifyFamily(ctx, h.database, f.ID)
		if err != nil {
			log.Printf("address family %s: verify: %v", f.Suffix, err)
			return
		}
		if !won {
			if _, err := db.SetFamilyCheck(ctx, h.database, f.ID, false, "*."+f.Suffix+" (or a name under it) is connected to another account"); err != nil {
				log.Printf("address family %s: %v", f.Suffix, err)
			}
			return
		}
		for _, l := range losers {
			h.removeFamilyRequest(l.Suffix)
			h.emailFamilyNotice(l, "Your address family *."+l.Suffix+" was not connected",
				fmt.Sprintf("You asked to connect *.%s to your Simple Host account, but another account proved it owns that name first, so your request was dropped.\n\nIf the name is yours, check who else has access to its DNS records.\n\nSimple Host\n", l.Suffix))
		}
		f.VerifiedAt = sql.NullTime{Time: time.Now(), Valid: true}
		if err := h.disk.BindFamily(f.UserID, f.Suffix); err != nil {
			log.Printf("address family %s: bind: %v", f.Suffix, err)
		}
		h.syncFamilyRequest(f)
		log.Printf("address family *.%s of %s verified", f.Suffix, f.UserID)
		h.refreshFamilies(ctx)
		return
	}
	// Verified: keep its link and request in step, then record the check.
	if err := h.disk.BindFamily(f.UserID, f.Suffix); err != nil {
		log.Printf("address family %s: bind: %v", f.Suffix, err)
	}
	h.syncFamilyRequest(f)
	check, err := db.SetFamilyCheck(ctx, h.database, f.ID, ok, why)
	if err != nil {
		log.Printf("address family %s: %v", f.Suffix, err)
		return
	}
	if ok || !check.FailingSince.Valid {
		return
	}
	lim := config.Active()
	failing := time.Since(check.FailingSince.Time)
	due := failing >= lim.FamilyLapseAfter
	if check.ReleaseAt.Valid {
		due = !time.Now().Before(check.ReleaseAt.Time)
	}
	switch {
	case due:
		if err := h.releaseFamily(ctx, f); err != nil {
			log.Printf("address family %s: lapse: %v", f.Suffix, err)
			return
		}
		h.emailFamilyNotice(f, "Your address family *."+f.Suffix+" was disconnected",
			fmt.Sprintf("*.%s failed every check for %s, so it was disconnected from your Simple Host account. Your sites answer at their own Simple Host addresses again.\n\nWhat the last check saw: %s\n\nConnecting it again needs its DNS ownership record (TXT %s) once more.\n\nSimple Host\n", f.Suffix, config.Span(failing.Round(time.Hour)), why, domainProofHost(f.Suffix)))
	case failing >= lim.FamilyLapseWarnAfter && !check.Notified:
		releaseAt := time.Now().Add(lim.FamilyLapseAfter - lim.FamilyLapseWarnAfter)
		if h.emailFamilyNotice(f, "Your address family *."+f.Suffix+" has stopped working",
			fmt.Sprintf("Your sites answer at <site>.%s, and that address family has failed every check for %s.\n\nWhat the last check saw: %s\n\nIf you moved the domain on purpose, there is nothing to do. Otherwise check its DNS records at your registrar (the wildcard record *.%s and the TXT record %s).\n\nIf it is still failing %s from now, *.%s is disconnected from your account and your sites answer at their own Simple Host addresses again.\n\nSimple Host\n",
				f.Suffix, config.Span(lim.FamilyLapseWarnAfter), why, f.Suffix, domainProofHost(f.Suffix), config.Span(lim.FamilyLapseAfter-lim.FamilyLapseWarnAfter), f.Suffix)) {
			if err := db.MarkFamilyLapseNotified(ctx, h.database, f.ID, releaseAt); err != nil {
				log.Printf("address family %s: mark notified: %v", f.Suffix, err)
			}
		}
	}
}

// emailFamilyNotice sends one plain notice to the family's owner.
func (h *SiteHandler) emailFamilyNotice(f db.AddressFamily, subject, text string) bool {
	mailer, ok := h.mailer.(noticeSender)
	if !ok || !strings.Contains(f.Email, "@") {
		return false
	}
	if err := mailer.SendNotice(f.Email, subject, text); err != nil {
		log.Printf("address family %s: notice: %v", f.Suffix, err)
		return false
	}
	return true
}

// --- Admin ---

// adminListFamilies GET /v1/admin/address-families.
func (h *SiteHandler) adminListFamilies(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	fams, err := db.ListAllFamilies(r.Context(), h.database)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]familyResponse, 0, len(fams))
	for _, f := range fams {
		resp := h.familyResponseFor(r.Context(), f, false)
		resp.Owner, resp.CertName = f.Email, f.CertName
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"address_families": out, "available": h.familiesOn()})
}

// adminCreateFamily POST /v1/admin/users/{id}/address-families.
func (h *SiteHandler) adminCreateFamily(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if !siteIDShape.MatchString(id) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "account not found"})
		return
	}
	h.createFamilyFor(w, r, id, true)
}

// adminFamily loads the family {id}.
func (h *SiteHandler) adminFamily(w http.ResponseWriter, r *http.Request) (db.AddressFamily, bool) {
	if !accountAdmin(w, r) {
		return db.AddressFamily{}, false
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if !siteIDShape.MatchString(id) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "address family not found"})
		return db.AddressFamily{}, false
	}
	f, err := db.GetFamilyByID(r.Context(), h.database, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "address family not found"})
		return db.AddressFamily{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return db.AddressFamily{}, false
	}
	return f, true
}

// adminSetFamilyCert PUT /v1/admin/address-families/{id}/cert-mode
// {"cert_mode":"wildcard","cert_name":"<lineage>"}: the operator's wildcard
// certificate for the family ("" takes it away).
func (h *SiteHandler) adminSetFamilyCert(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	var req struct {
		CertMode string `json:"cert_mode"`
		CertName string `json:"cert_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	switch req.CertMode {
	case "wildcard", "":
	case "per_host":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "certificates per site name (per_host) are not available yet", Code: "cert_mode_unavailable"})
		return
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cert_mode must be wildcard", Code: "invalid_cert_mode"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.CertName))
	if name != "" && !certNameRE.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cert_name is a certificate lineage name (letters, digits, dots, hyphens)", Code: "invalid_cert_name"})
		return
	}
	if err := db.SetFamilyCert(r.Context(), h.database, f.ID, "wildcard", name); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("admin %s set address family *.%s certificate to wildcard %q", adminName(r), f.Suffix, name)
	f, _ = db.GetFamilyByID(r.Context(), h.database, f.ID)
	h.syncFamilyRequest(f)
	h.refreshFamilies(r.Context())
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.CertName = f.Email, f.CertName
	writeJSON(w, http.StatusOK, resp)
}

// adminSetFamilyProofExempt PUT /v1/admin/address-families/{id}/proof-exempt
// {"proof_exempt":true}: verify without the TXT record (a family the
// operator already runs; the wildcard must still point here).
func (h *SiteHandler) adminSetFamilyProofExempt(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	var req struct {
		ProofExempt *bool `json:"proof_exempt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.ProofExempt == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "proof_exempt (true or false) is required"})
		return
	}
	if err := db.SetFamilyProofExempt(r.Context(), h.database, f.ID, *req.ProofExempt); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("admin %s set address family *.%s proof exempt to %v", adminName(r), f.Suffix, *req.ProofExempt)
	f, _ = db.GetFamilyByID(r.Context(), h.database, f.ID)
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.CertName = f.Email, f.CertName
	writeJSON(w, http.StatusOK, resp)
}

// adminDeleteFamily DELETE /v1/admin/address-families/{id}: disconnect (an
// abuse action as much as a support one).
func (h *SiteHandler) adminDeleteFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	if err := h.releaseFamily(r.Context(), f); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("admin %s disconnected address family *.%s of %s", adminName(r), f.Suffix, f.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// adminCheckFamily POST /v1/admin/address-families/{id}/check: check now.
func (h *SiteHandler) adminCheckFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	if h.familiesOn() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*domainProbeTimeout)
		defer cancel()
		h.checkFamily(ctx, f, h.serverAddrs(ctx))
	}
	f, err := db.GetFamilyByID(r.Context(), h.database, f.ID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "the family was let go"})
		return
	}
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.CertName = f.Email, f.CertName
	writeJSON(w, http.StatusOK, resp)
}

// registerFamilyRoutes adds the family API to the mux.
func (h *SiteHandler) registerFamilyRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	checkIP := h.familyCheckLimiter
	if checkIP == nil {
		checkIP = newRateLimiterFor(config.Active().RateFamilyCheck)
	}
	mux.Handle("POST /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createFamily))))
	mux.Handle("GET /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listFamilies))))
	mux.Handle("GET /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getFamily))))
	mux.Handle("PATCH /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.patchFamily))))
	mux.Handle("DELETE /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteFamily))))
	mux.Handle("POST /v1/me/address-families/{suffix}/check", noticeMiddleware(authMiddleware(rateLimitByIP(checkIP, http.HandlerFunc(h.checkFamilyNow)))))
	mux.Handle("GET /v1/admin/address-families", authMiddleware(http.HandlerFunc(h.adminListFamilies)))
	mux.Handle("POST /v1/admin/users/{id}/address-families", authMiddleware(http.HandlerFunc(h.adminCreateFamily)))
	mux.Handle("PUT /v1/admin/address-families/{id}/cert-mode", authMiddleware(http.HandlerFunc(h.adminSetFamilyCert)))
	mux.Handle("PUT /v1/admin/address-families/{id}/proof-exempt", authMiddleware(http.HandlerFunc(h.adminSetFamilyProofExempt)))
	mux.Handle("POST /v1/admin/address-families/{id}/check", authMiddleware(http.HandlerFunc(h.adminCheckFamily)))
	mux.Handle("DELETE /v1/admin/address-families/{id}", authMiddleware(http.HandlerFunc(h.adminDeleteFamily)))
	mux.HandleFunc("GET /internal/family/{rest...}", h.familyPageHandler)
}
