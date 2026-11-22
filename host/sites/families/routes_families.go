package families

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
	"regexp"
	"strings"
	"time"

	notifications "github.com/vsriram/simple-host/host/data/notify"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
	"golang.org/x/net/publicsuffix"
)

var familyCertNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// familyPrefixRE is a family's site-name prefix ("" or up to 40 characters,
// starting with a letter or digit; it may end with a hyphen: voucher-).
var familyPrefixRE = regexp.MustCompile(`^(?:[a-z0-9][a-z0-9-]{0,39})?$`)

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

func (h Handler) isPlatformZoneHost(host string) bool {
	if sitesroute.IsOwnHost(host, h.ContentHost) {
		return true
	}
	for _, b := range h.Hosts.Addressing().KnownBases() {
		if sitesroute.IsOwnHost(host, b) {
			return true
		}
	}
	for _, z := range h.Hosts.PlatformZones() {
		if sitesroute.IsOwnHost(host, z) {
			return true
		}
	}
	if t := strings.ToLower(strings.TrimSpace(h.CNAMETarget)); t != "" {
		if reg, err := publicsuffix.EffectiveTLDPlusOne(t); err == nil && sitesroute.IsOwnHost(host, reg) {
			return true
		}
	}
	return false
}

// normalizeFamilySuffix cleans and checks the name of a family: "*.brand.com",
// "brand.com" or a pasted URL all mean the suffix brand.com. It must be a
// valid ASCII domain of at least two labels that is not a public suffix
// itself and not the platform's own.
func (h Handler) normalizeFamilySuffix(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "*.")
	suffix, err := h.NormalizeDomain(s)
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

type familySite struct {
	Site    string `json:"site"`
	Address string `json:"address"`
	Main    bool   `json:"main"`
	// LivesAt: the site lives on a domain of its own; this address redirects there.
	LivesAt string `json:"lives_at,omitempty"`
}

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
	DNS         *sitesroute.DNSRecord `json:"dns,omitempty"`
	DNSA        *sitesroute.DNSRecord `json:"dns_a,omitempty"`
	DNSTXT      *sitesroute.DNSRecord `json:"dns_txt,omitempty"`
	ProofExempt bool                  `json:"proof_exempt,omitempty"`
	Certificate familyCert            `json:"certificate"`
	ExampleURL  string                `json:"example_url,omitempty"`
	Sites       []familySite          `json:"sites,omitempty"`
	Owner       string                `json:"owner,omitempty"`
	OwnerID     string                `json:"owner_id,omitempty"`
	CertName    string                `json:"cert_name,omitempty"`
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
func (h Handler) familyCertOf(f model.AddressFamily) familyCert {
	c := familyCert{Mode: f.CertMode}
	switch {
	case f.CertMode != "wildcard":
		c.Status, c.Note = "failed", "certificates per site name are not available yet"
	case f.CertName == "":
		c.Status, c.Note = "waiting_for_operator", "the operator sets up the wildcard certificate for *."+f.Suffix+"; write to "+auth.SupportContact+" if it has been more than a day"
	case !f.VerifiedAt.Valid:
		c.Status = "pending"
	case h.Hosts.FamilyIsLive(f):
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
		if b, err := os.ReadFile(filepath.Join(h.Hosts.FamilyCertDir(), "failed", f.Suffix)); err == nil && sites.ValidFamilyKey(f.Suffix) {
			c.Status, c.Note = "failed", strings.TrimSpace(string(b))
		}
	}
	return c
}

func (h Handler) familyReadyValue(suffix, key string) string {
	if h.Hosts.FamilyCertDir() == "" || !sites.ValidFamilyKey(suffix) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(h.Hosts.FamilyCertDir(), "ready", suffix))
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

func (h Handler) familyResponseFor(ctx context.Context, f model.AddressFamily, withSites bool) familyResponse {
	resp := familyResponse{
		ID: f.ID, Family: "*." + f.Suffix, Suffix: f.Suffix, SitePrefix: f.SitePrefix, Rank: f.Rank,
		Canonical: f.Canonical, Status: f.Status, Live: h.Hosts.FamilyServed(f), LastError: f.LastError,
		BoundAt: f.BoundAt, VerifiedAt: timePtr(f.VerifiedAt), FailingSince: timePtr(f.FailingSince),
		ProofExempt: f.ProofExempt, Certificate: h.familyCertOf(f),
	}
	if f.LapseNotifiedAt.Valid {
		resp.ReleaseAt = timePtr(f.ReleaseAt)
	}
	if !f.VerifiedAt.Valid {
		t := f.BoundAt.Add(h.Limits.FamilyUnprovenTTL)
		resp.ExpiresAt = &t
	}
	resp.DNS = &sitesroute.DNSRecord{Type: "CNAME", Host: "*." + f.Suffix, Value: h.CNAMETarget}
	if h.CustomDomainIP != "" {
		resp.DNSA = &sitesroute.DNSRecord{Type: "A", Host: "*." + f.Suffix, Value: h.CustomDomainIP}
	}
	if !f.ProofExempt {
		resp.DNSTXT = &sitesroute.DNSRecord{Type: "TXT", Host: sitesroute.DomainProofHost(f.Suffix), Value: f.Token}
	}
	if owned, err := h.SitesSvc().ListSitesByUser(ctx, f.UserID); err == nil {
		for _, s := range owned {
			label, ok := strings.CutPrefix(s.Name, f.SitePrefix)
			if !ok || !sites.FamilyLabelOK(f, label, h.Limits) {
				continue
			}
			if resp.ExampleURL == "" {
				resp.ExampleURL = "https://" + label + "." + f.Suffix + "/"
			}
			if !withSites {
				break
			}
			fs := familySite{Site: s.Name, Address: "https://" + label + "." + f.Suffix + "/"}
			if own, has, err := h.Hosts.SiteOwnDomain(ctx, s.ID); err == nil && has {
				fs.LivesAt = "https://" + strings.ToLower(own.Domain) + "/"
			} else if main, ok := h.Hosts.SiteFamilyAddress(s.UserID, s.Name); ok && main == label+"."+f.Suffix {
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

func (h Handler) familiesAvailable(w http.ResponseWriter) bool {
	if h.Hosts.FamiliesOn() {
		return true
	}
	httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "address families are not available on this server", Code: "address_families_unavailable"})
	return false
}

func validRank(n int) bool { return n >= 0 && n <= 1000 }

func (h Handler) createFamily(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	h.createFamilyFor(w, r, user.ID, false)
}

// createFamilyFor connects a family for userID (admin: with the operator's
// certificate and the proof exemption).
func (h Handler) createFamilyFor(w http.ResponseWriter, r *http.Request, userID string, admin bool) {
	if !h.familiesAvailable(w) {
		return
	}
	if u, err := h.StoreAccounts().GetUserByID(r.Context(), userID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "account not found"})
		return
	} else if u.Suspended {
		auth.WriteSuspended(w, "", "")
		return
	}
	var req familyRequest
	if !httpx.DecodeJSON(w, r, 8<<10, &req) {
		return
	}
	raw := req.Suffix
	if raw == "" {
		raw = req.Family
	}
	suffix, err := h.normalizeFamilySuffix(raw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: err.Error(), Code: "invalid_domain"})
		return
	}
	switch req.CertMode {
	case "", "wildcard":
	case "per_host":
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "certificates per site name (cert_mode per_host) are not available yet: address families use a wildcard certificate that the operator sets up for *." + suffix + " (write to " + auth.SupportContact + ")", Code: "cert_mode_unavailable"})
		return
	default:
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "cert_mode must be wildcard", Code: "invalid_cert_mode"})
		return
	}
	prefix := strings.ToLower(strings.TrimSpace(req.SitePrefix))
	if !familyPrefixRE.MatchString(prefix) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site_prefix may use lowercase letters, digits and hyphens (not at the start), up to 40 characters", Code: "invalid_site_prefix"})
		return
	}
	rank := 0
	if req.Rank != nil {
		rank = *req.Rank
	}
	if !validRank(rank) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "rank must be between 0 and 1000", Code: "invalid_rank"})
		return
	}
	canonical := true
	if req.Canonical != nil {
		canonical = *req.Canonical
	}
	b := model.FamilyBind{UserID: userID, Suffix: suffix, SitePrefix: prefix, Rank: rank, Canonical: canonical,
		CertMode: "wildcard", Max: h.Limits.FamiliesPerAccount}
	if admin {
		name := strings.ToLower(strings.TrimSpace(req.CertName))
		if name != "" && !certNameRE.MatchString(name) {
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "cert_name is a certificate lineage name (letters, digits, dots, hyphens)", Code: "invalid_cert_name"})
			return
		}
		b.CertName, b.ProofExempt = name, req.ProofExempt
	}
	f, created, err := h.svc().ConnectFamily(r.Context(), b)
	switch {
	case err == nil && !created:
		httpx.WriteJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
		return
	case errors.Is(err, model.ErrFamilyTaken):
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "*." + suffix + " (or a name under it) is connected to another account", Code: "domain_taken"})
		return
	case errors.Is(err, model.ErrFamilyCap):
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: fmt.Sprintf("an account may connect %d address families on this server", h.Limits.FamiliesPerAccount), Code: "address_family_limit"})
		return
	}
	if err != nil {
		log.Printf("address family %s: bind: %v", suffix, err)
		httpx.WriteInternal(w)
		return
	}
	if admin {
		log.Printf("admin %s connected address family *.%s for %s (cert %q, proof exempt %v)", adminName(r), suffix, userID, f.CertName, f.ProofExempt)
	}
	httpx.WriteJSON(w, http.StatusCreated, h.familyResponseFor(r.Context(), f, false))
}

var certNameRE = familyCertNameRE

func adminName(r *http.Request) string {
	if u := auth.GetUser(r.Context()); u != nil {
		return u.Username
	}
	return "?"
}

func (h Handler) listFamilies(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	fams, err := h.SitesSvc().ListFamiliesByUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]familyResponse, 0, len(fams))
	for _, f := range fams {
		out = append(out, h.familyResponseFor(r.Context(), f, false))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"address_families": out, "available": h.Hosts.FamiliesOn()})
}

// callerFamily loads the caller's family named by {suffix} (either form).
func (h Handler) callerFamily(w http.ResponseWriter, r *http.Request) (model.AddressFamily, bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return model.AddressFamily{}, false
	}
	raw, _ := url.PathUnescape(r.PathValue("suffix"))
	suffix := sitesroute.DomainCandidate(strings.TrimPrefix(strings.TrimSpace(raw), "*."))
	f, err := h.svc().GetFamilyByUserSuffix(r.Context(), user.ID, suffix)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "no address family *." + suffix + " on this account", Code: "no_address_family"})
		return model.AddressFamily{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return model.AddressFamily{}, false
	}
	return f, true
}

func (h Handler) getFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, r.URL.Query().Get("sites") == "1"))
}

type familyPatchRequest struct {
	SitePrefix *string `json:"site_prefix"`
	Rank       *int    `json:"rank"`
	Canonical  *bool   `json:"canonical"`
}

func (h Handler) patchFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	var req familyPatchRequest
	if !httpx.DecodeJSON(w, r, 8<<10, &req) {
		return
	}
	if req.SitePrefix != nil {
		p := strings.ToLower(strings.TrimSpace(*req.SitePrefix))
		if !familyPrefixRE.MatchString(p) {
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site_prefix may use lowercase letters, digits and hyphens (not at the start), up to 40 characters", Code: "invalid_site_prefix"})
			return
		}
		req.SitePrefix = &p
	}
	if req.Rank != nil && !validRank(*req.Rank) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "rank must be between 0 and 1000", Code: "invalid_rank"})
		return
	}
	f, err := h.svc().UpdateFamily(r.Context(), f.UserID, f.Suffix, model.FamilyPatch{SitePrefix: req.SitePrefix, Rank: req.Rank, Canonical: req.Canonical})
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
}

func (h Handler) deleteFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	if err := h.svc().ReleaseFamily(r.Context(), f); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) checkFamilyNow(w http.ResponseWriter, r *http.Request) {
	f, ok := h.callerFamily(w, r)
	if !ok {
		return
	}
	if h.FamilyCheckUserLimiter != nil && !h.FamilyCheckUserLimiter.AllowHTTP(w, f.UserID) {
		httpx.TooManyRequests(w)
		return
	}
	if h.Hosts.FamiliesOn() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*sitesroute.DomainProbeTimeout)
		defer cancel()
		h.CheckFamily(ctx, f, h.Hosts.ServerAddrs(ctx))
		h.Hosts.RefreshFamilies(ctx)
	}
	f, err := h.SitesSvc().GetFamilyByID(r.Context(), f.ID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "the family was let go", Code: "no_address_family"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.familyResponseFor(r.Context(), f, false))
}

// CheckFamilies is one pass: drop pending families that never proved
// themselves, then check every family that is due.
func (h Handler) CheckFamilies(ctx context.Context) {
	if !h.Hosts.FamiliesOn() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sitesroute.DomainPassTimeout)
	defer cancel()
	expired, err := h.svc().ReleaseExpiredFamilies(ctx, h.Limits.FamilyUnprovenTTL)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "check address family", err)
	}
	if len(expired) > 0 {
		sites.ReportWorkerProgress(ctx)
	}
	for _, f := range expired {
		h.Hosts.RemoveFamilyRequest(ctx, f.Suffix)
		log.Printf("address family *.%s: never proved; dropped", f.Suffix)
	}
	due, err := h.svc().ListFamiliesToCheck(ctx, h.Limits.FamilyActiveRecheck)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "check address family", err)
		return
	}
	ours := h.Hosts.ServerAddrs(ctx)
	for _, f := range due {
		h.CheckFamily(ctx, f, ours)
	}
	h.Hosts.RefreshFamilies(ctx)
}

// familyProbeLabel is a label nobody has: the wildcard record must answer it.
func familyProbeLabel() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "sh-check-" + hex.EncodeToString(b)
}

// verifyFamilyDNS: the ownership record (unless exempt) and the wildcard:
// a label nobody has resolves here and only here.
func (h Handler) verifyFamilyDNS(ctx context.Context, f model.AddressFamily, ours map[string]bool) (bool, string) {
	if !f.ProofExempt {
		if ok, why := sitesroute.DomainProof(ctx, f.Suffix, f.Token); !ok {
			return false, why
		}
	}
	probe := familyProbeLabel() + "." + f.Suffix
	ips, err := sites.LookupHost(ctx, probe)
	if err != nil || len(ips) == 0 {
		return false, "the wildcard record *." + f.Suffix + " is not seen yet (add it at your registrar: " + h.familyRecordHint(f) + ")"
	}
	here := false
	for _, ip := range ips {
		if !ours[ip] {
			return false, "*." + f.Suffix + " resolves to " + ip + ", which is not this server (remove any other A or AAAA record for *." + f.Suffix + ")"
		}
		if notifications.PublicIP(ip) {
			here = true
		}
	}
	if !here {
		return false, "*." + f.Suffix + " does not resolve to this server yet"
	}
	return true, ""
}

func (h Handler) familyRecordHint(f model.AddressFamily) string {
	if h.CNAMETarget != "" {
		return "CNAME *." + f.Suffix + " -> " + h.CNAMETarget
	}
	return "A *." + f.Suffix + " -> " + h.CustomDomainIP
}

// probeFamily: one of the account's sites answers over HTTPS at its family
// address, through this server (any HTTP answer counts: a site may be
// offline or locked).
func (h Handler) probeFamily(ctx context.Context, f model.AddressFamily, ours map[string]bool) (bool, string) {
	owned, err := h.SitesSvc().ListSitesByUser(ctx, f.UserID)
	if err != nil {
		return true, ""
	}
	for _, s := range owned {
		label, ok := strings.CutPrefix(s.Name, f.SitePrefix)
		if !ok || !sites.FamilyLabelOK(f, label, h.Limits) {
			continue
		}
		host := label + "." + f.Suffix
		ips, err := sites.LookupHost(ctx, host)
		if err != nil {
			return false, host + " does not resolve"
		}
		ip := ""
		for _, a := range ips {
			if ours[a] && notifications.PublicIP(a) {
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
		resp, err := sitesroute.DomainProbeVia(ip).Do(req)
		if err != nil {
			return false, "https://" + host + "/ does not answer over HTTPS"
		}
		resp.Body.Close()
		return true, ""
	}
	return true, "" // no site to probe yet
}

// CheckFamily proves one family and acts on it: a pending one that passes
// is verified (the first account to prove a name wins it), a verified one
// that keeps failing is warned about and then let go.
func (h Handler) CheckFamily(ctx context.Context, f model.AddressFamily, ours map[string]bool) {
	ok, why := h.verifyFamilyDNS(ctx, f, ours)
	if ok && f.VerifiedAt.Valid && h.Hosts.FamilyIsLive(f) {
		ok, why = h.probeFamily(ctx, f, ours)
	}
	if !f.VerifiedAt.Valid {
		if !ok {
			if _, err := h.svc().SetFamilyCheck(ctx, f.ID, false, why); err != nil {
				sites.ReportWorkerFailure(ctx, "check address family", err)
			}
			return
		}
		won, losers, err := h.svc().VerifyFamily(ctx, f.ID)
		if err != nil {
			sites.ReportWorkerFailure(ctx, "check address family", err)
			return
		}
		if !won {
			if _, err := h.svc().SetFamilyCheck(ctx, f.ID, false, "*."+f.Suffix+" (or a name under it) is connected to another account"); err != nil {
				sites.ReportWorkerFailure(ctx, "check address family", err)
			}
			return
		}
		for _, l := range losers {
			h.Hosts.RemoveFamilyRequest(ctx, l.Suffix)
			h.emailFamilyNotice(ctx, l, "Your address family *."+l.Suffix+" was not connected",
				fmt.Sprintf("You asked to connect *.%s to your Simple Host account, but another account proved it owns that name first, so your request was dropped.\n\nIf the name is yours, check who else has access to its DNS records.\n\nSimple Host\n", l.Suffix))
		}
		f.VerifiedAt = sql.NullTime{Time: time.Now(), Valid: true}
		if err := h.Disk.BindFamily(f.UserID, f.Suffix); err != nil {
			sites.ReportWorkerFailure(ctx, "check address family", err)
		}
		h.svc().SyncFamilyRequest(ctx, f)
		sites.ReportWorkerProgress(ctx)
		log.Printf("address family *.%s of %s verified", f.Suffix, f.UserID)
		h.Hosts.RefreshFamilies(ctx)
		return
	}
	// Verified: keep its link and request in step, then record the check.
	if err := h.Disk.BindFamily(f.UserID, f.Suffix); err != nil {
		sites.ReportWorkerFailure(ctx, "check address family", err)
	}
	h.svc().SyncFamilyRequest(ctx, f)
	check, err := h.svc().SetFamilyCheck(ctx, f.ID, ok, why)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "check address family", err)
		return
	}
	sites.ReportWorkerProgress(ctx)
	if ok || !check.FailingSince.Valid {
		return
	}
	lim := h.Limits
	failing := time.Since(check.FailingSince.Time)
	due := failing >= lim.FamilyLapseAfter
	if check.ReleaseAt.Valid {
		due = !time.Now().Before(check.ReleaseAt.Time)
	}
	switch {
	case due:
		if err := h.svc().ReleaseFamily(ctx, f); err != nil {
			sites.ReportWorkerFailure(ctx, "check address family", err)
			return
		}
		h.emailFamilyNotice(ctx, f, "Your address family *."+f.Suffix+" was disconnected",
			fmt.Sprintf("*.%s failed every check for %s, so it was disconnected from your Simple Host account. Your sites answer at their own Simple Host addresses again.\n\nWhat the last check saw: %s\n\nConnecting it again needs its DNS ownership record (TXT %s) once more.\n\nSimple Host\n", f.Suffix, web.LimitSpan(failing.Round(time.Hour)), why, sitesroute.DomainProofHost(f.Suffix)))
	case failing >= lim.FamilyLapseWarnAfter && !check.Notified:
		releaseAt := time.Now().Add(lim.FamilyLapseAfter - lim.FamilyLapseWarnAfter)
		if h.emailFamilyNotice(ctx, f, "Your address family *."+f.Suffix+" has stopped working",
			fmt.Sprintf("Your sites answer at <site>.%s, and that address family has failed every check for %s.\n\nWhat the last check saw: %s\n\nIf you moved the domain on purpose, there is nothing to do. Otherwise check its DNS records at your registrar (the wildcard record *.%s and the TXT record %s).\n\nIf it is still failing %s from now, *.%s is disconnected from your account and your sites answer at their own Simple Host addresses again.\n\nSimple Host\n",
				f.Suffix, web.LimitSpan(lim.FamilyLapseWarnAfter), why, f.Suffix, sitesroute.DomainProofHost(f.Suffix), web.LimitSpan(lim.FamilyLapseAfter-lim.FamilyLapseWarnAfter), f.Suffix)) {
			if err := h.svc().MarkFamilyLapseNotified(ctx, f.ID, releaseAt); err != nil {
				sites.ReportWorkerFailure(ctx, "check address family", err)
			}
		}
	}
}

func (h Handler) emailFamilyNotice(ctx context.Context, f model.AddressFamily, subject, text string) bool {
	return (&notifications.StoreMail{Mailer: h.Mailer}).FamilyNotice(ctx, f, subject, text)
}

func (h Handler) adminListFamilies(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	fams, err := h.SitesSvc().ListAllFamilies(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]familyResponse, 0, len(fams))
	for _, f := range fams {
		resp := h.familyResponseFor(r.Context(), f, false)
		resp.Owner, resp.OwnerID, resp.CertName = f.Email, f.UserID, f.CertName
		out = append(out, resp)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"address_families": out, "available": h.Hosts.FamiliesOn()})
}

func (h Handler) adminCreateFamily(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if !sitesroute.SiteIDShape.MatchString(id) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "account not found"})
		return
	}
	h.createFamilyFor(w, r, id, true)
}

func (h Handler) adminFamily(w http.ResponseWriter, r *http.Request) (model.AddressFamily, bool) {
	if !auth.Admin(w, r) {
		return model.AddressFamily{}, false
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if !sitesroute.SiteIDShape.MatchString(id) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "address family not found"})
		return model.AddressFamily{}, false
	}
	f, err := h.SitesSvc().GetFamilyByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "address family not found"})
		return model.AddressFamily{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return model.AddressFamily{}, false
	}
	return f, true
}

// adminSetFamilyCert PUT /v1/admin/address-families/{id}/cert-mode
// {"cert_mode":"wildcard","cert_name":"<lineage>"}: the operator's wildcard
// certificate for the family ("" takes it away).
func (h Handler) adminSetFamilyCert(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	var req struct {
		CertMode string `json:"cert_mode"`
		CertName string `json:"cert_name"`
	}
	if !httpx.DecodeJSON(w, r, 4<<10, &req) {
		return
	}
	switch req.CertMode {
	case "wildcard", "":
	case "per_host":
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "certificates per site name (per_host) are not available yet", Code: "cert_mode_unavailable"})
		return
	default:
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "cert_mode must be wildcard", Code: "invalid_cert_mode"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.CertName))
	if name != "" && !certNameRE.MatchString(name) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "cert_name is a certificate lineage name (letters, digits, dots, hyphens)", Code: "invalid_cert_name"})
		return
	}
	suffix := f.Suffix
	f, err := h.svc().SetFamilyCert(r.Context(), f.ID, "wildcard", name)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	log.Printf("admin %s set address family *.%s certificate to wildcard %q", adminName(r), suffix, name)
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.OwnerID, resp.CertName = f.Email, f.UserID, f.CertName
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// adminSetFamilyProofExempt PUT /v1/admin/address-families/{id}/proof-exempt
// {"proof_exempt":true}: verify without the TXT record (a family the
// operator already runs; the wildcard must still point here).
func (h Handler) adminSetFamilyProofExempt(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	var req struct {
		ProofExempt *bool `json:"proof_exempt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.ProofExempt == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "proof_exempt (true or false) is required"})
		return
	}
	suffix := f.Suffix
	f, err := h.svc().SetFamilyProofExempt(r.Context(), f.ID, *req.ProofExempt)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	log.Printf("admin %s set address family *.%s proof exempt to %v", adminName(r), suffix, *req.ProofExempt)
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.OwnerID, resp.CertName = f.Email, f.UserID, f.CertName
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// adminDeleteFamily DELETE /v1/admin/address-families/{id}: disconnect (an
// abuse action as much as a support one).
func (h Handler) adminDeleteFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	if err := h.svc().ReleaseFamily(r.Context(), f); err != nil {
		httpx.WriteInternal(w)
		return
	}
	log.Printf("admin %s disconnected address family *.%s of %s", adminName(r), f.Suffix, f.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) adminCheckFamily(w http.ResponseWriter, r *http.Request) {
	f, ok := h.adminFamily(w, r)
	if !ok {
		return
	}
	if h.Hosts.FamiliesOn() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*sitesroute.DomainProbeTimeout)
		defer cancel()
		h.CheckFamily(ctx, f, h.Hosts.ServerAddrs(ctx))
		h.Hosts.RefreshFamilies(ctx)
	}
	f, err := h.SitesSvc().GetFamilyByID(r.Context(), f.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "the family was let go"})
		return
	}
	resp := h.familyResponseFor(r.Context(), f, false)
	resp.Owner, resp.OwnerID, resp.CertName = f.Email, f.UserID, f.CertName
	httpx.WriteJSON(w, http.StatusOK, resp)
}
