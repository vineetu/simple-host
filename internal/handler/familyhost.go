package handler

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Address families (owner decision 2026-09-29; INTENT.md).
//
// An account connects *.<suffix> once (db.AddressFamily). Every site of that
// account named <prefix><label> then answers at <label>.<suffix>, where
// <prefix> is the family's optional site-name prefix. Nothing else of the
// account, and nothing of any other account, is ever reachable there: the
// host alone names the account (the family's owner) and the site.
//
// A family serves only once it is verified (its TXT record, or the admin's
// proof exemption, plus the wildcard pointing here) AND its certificate is
// live: in release 1 the operator's wildcard certificate (cert_mode
// wildcard, lineage cert_name), which the root issuer (deploy/family-certs)
// checks before it writes the family's nginx server and its ready marker
// ($ADDRESS_FAMILY_CERT_DIR/ready/<suffix>). Until then no address under it
// is answered, handed out or redirected to.
//
// Serving: nginx serves files straight from disk (families/<suffix> ->
// by-id/<user>) with the take-down, offline, passcode and lives-elsewhere
// markers, and hands /v1/, /internal/ and misses to the app, where
// FamilyHosts answers them. A family address is the site's main address
// (canonical, the default) unless the site has a custom domain or a claimed
// name: ranking is own domain > the most specific canonical family > the
// site host. Every family address of a site keeps working either way.

// familyLive is a family whose addresses are answered: verified, and its
// certificate live with the site prefix the database has now.
type familyIndex struct {
	mu       sync.RWMutex
	bySuffix map[string]db.AddressFamily
	byUser   map[string][]db.AddressFamily
}

// familyLabelRE is one DNS label.
var familyLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// familyCertNameRE is a certbot lineage name (the operator's certificate).
var familyCertNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// familyPrefixRE is a family's site-name prefix ("" or up to 40 characters,
// starting with a letter or digit; it may end with a hyphen: voucher-).
var familyPrefixRE = regexp.MustCompile(`^(?:[a-z0-9][a-z0-9-]{0,39})?$`)

// SetAddressFamilies sets the issuer hand-off directory and starts keeping
// the list of live families (and checking them, when checks run here).
func (h *SiteHandler) SetAddressFamilies(certDir string) {
	h.familyCertDir = strings.TrimSpace(certDir)
	lim := config.Active()
	h.familyCheckLimiter = newRateLimiterFor(lim.RateFamilyCheck)
	h.familyCheckLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	h.familyCheckUserLimiter = newRateLimiterFor(lim.RateFamilyCheckUser)
	h.familyCheckUserLimiter.startCleanup(10*time.Minute, 30*time.Minute)
}

// familiesOn: families may exist and be served here.
func (h *SiteHandler) familiesOn() bool {
	return config.Active().AddressFamilies && h.familyCertDir != ""
}

// StartAddressFamilies loads the live families now and keeps them fresh,
// and runs the families' DNS checks, until ctx ends.
func (h *SiteHandler) StartAddressFamilies(ctx context.Context) {
	if !h.familiesOn() {
		return
	}
	h.refreshFamilies(ctx)
	go func() {
		t := time.NewTicker(config.Active().FamilyCacheTTL)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.refreshFamilies(ctx)
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		for {
			h.checkFamilies(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(config.Active().FamilyCheckInterval):
			}
		}
	}()
}

// familyReadyPrefix reads the issuer's ready marker for suffix: whether it
// exists, and the site prefix the served configuration maps labels with.
func (h *SiteHandler) familyReadyPrefix(suffix string) (string, bool) {
	if h.familyCertDir == "" || !validFamilyKey(suffix) {
		return "", false
	}
	f, err := os.Open(filepath.Join(h.familyCertDir, "ready", suffix))
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "prefix="); ok {
			return p, true
		}
	}
	return "", false
}

// validFamilyKey: a suffix that is safe as a file name.
func validFamilyKey(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/\\") && !strings.HasPrefix(s, ".") && !strings.Contains(s, "..")
}

// familyIsLive: verified, wildcard certificate named, and the issuer serves
// it with the prefix the database has now.
func (h *SiteHandler) familyIsLive(f db.AddressFamily) bool {
	if !f.VerifiedAt.Valid || f.CertMode != "wildcard" || f.CertName == "" {
		return false
	}
	p, ok := h.familyReadyPrefix(f.Suffix)
	return ok && p == f.SitePrefix
}

// refreshFamilies reloads the live families. The content-host redirect
// markers of every account whose live families changed follow.
func (h *SiteHandler) refreshFamilies(ctx context.Context) {
	bySuffix := map[string]db.AddressFamily{}
	byUser := map[string][]db.AddressFamily{}
	if h.familiesOn() {
		list, err := db.ListVerifiedFamilies(ctx, h.database)
		if err != nil {
			log.Printf("address families: list: %v", err)
			return
		}
		for _, f := range list {
			if !h.familyIsLive(f) {
				continue
			}
			bySuffix[f.Suffix] = f
			byUser[f.UserID] = append(byUser[f.UserID], f)
		}
	}
	sig := func(fs []db.AddressFamily) string {
		var b strings.Builder
		for _, f := range fs {
			b.WriteString(f.Suffix + "|" + f.SitePrefix + "|" + boolStr(f.Canonical) + "|" + strconv.Itoa(f.Rank) + ";")
		}
		return b.String()
	}
	h.families.mu.Lock()
	old := h.families.byUser
	h.families.bySuffix, h.families.byUser = bySuffix, byUser
	h.families.mu.Unlock()
	changed := map[string]bool{}
	for u, fs := range byUser {
		if sig(old[u]) != sig(fs) {
			changed[u] = true
		}
	}
	for u, fs := range old {
		if sig(byUser[u]) != sig(fs) {
			changed[u] = true
		}
	}
	for u := range changed {
		h.syncUserRedirects(ctx, u)
	}
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// syncUserRedirects re-points the content-host redirect of every site of an
// account (its main address may have changed).
func (h *SiteHandler) syncUserRedirects(ctx context.Context, userID string) {
	sites, err := db.ListSitesByUser(ctx, h.database, userID)
	if err != nil {
		log.Printf("address families: sites of %s: %v", userID, err)
		return
	}
	for _, s := range sites {
		h.syncDomainRedirect(ctx, s.ID)
	}
}

// liveFamily is the live family a host is directly under, and the host's
// first label. Purely the index: no database.
func (h *SiteHandler) liveFamily(host string) (db.AddressFamily, string, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	label, suffix, ok := strings.Cut(host, ".")
	if !ok || label == "" {
		return db.AddressFamily{}, "", false
	}
	h.families.mu.RLock()
	f, ok := h.families.bySuffix[suffix]
	h.families.mu.RUnlock()
	if !ok {
		return db.AddressFamily{}, "", false
	}
	return f, label, true
}

// isFamilyHostName: host is <label>.<a live family's suffix>, whatever the
// label. Such a host is never a platform host (families are never under
// the platform's zones) and all of a family's hosts are the same "site" to
// a browser.
func (h *SiteHandler) isFamilyHostName(host string) bool {
	_, _, ok := h.liveFamily(host)
	return ok
}

// familyLabelOK: label may name a site under f: one DNS label, not an
// internationalised one, not reserved, and <prefix><label> is a site name.
func familyLabelOK(f db.AddressFamily, label string) bool {
	if !familyLabelRE.MatchString(label) || strings.HasPrefix(label, "xn--") {
		return false
	}
	for _, r := range config.Active().ReservedFamilyLabels() {
		if label == r {
			return false
		}
	}
	return validSiteName.MatchString(f.SitePrefix + label)
}

// familyMatch is what a family host names.
type familyMatch struct {
	Family db.AddressFamily
	Label  string
	Site   db.Site
}

// Host is the family address of the matched site.
func (m familyMatch) Host() string { return m.Label + "." + m.Family.Suffix }

// familySiteForHost resolves a family host to its site: the family's
// owner's live site named <prefix><label>. ok=false when the host is not a
// family host, the label cannot name a site, or there is no such site. A
// host that is also a custom domain bound here is that domain's, not the
// family's (the exact name wins).
func (h *SiteHandler) familySiteForHost(ctx context.Context, host string) (familyMatch, bool, error) {
	f, label, ok := h.liveFamily(host)
	if !ok || !familyLabelOK(f, label) {
		return familyMatch{}, false, nil
	}
	if _, err := db.GetSiteByCustomDomain(ctx, h.database, requestHostLower(host)); err == nil {
		return familyMatch{}, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return familyMatch{}, false, err
	}
	site, err := db.GetSiteByUser(ctx, h.database, f.UserID, f.SitePrefix+label)
	if errors.Is(err, sql.ErrNoRows) {
		return familyMatch{}, false, nil
	}
	if err != nil {
		return familyMatch{}, false, err
	}
	return familyMatch{Family: f, Label: label, Site: site}, true, nil
}

// isBoundDomain: host is a custom domain (or earlier address) bound here.
func (h *SiteHandler) isBoundDomain(ctx context.Context, host string) bool {
	_, err := db.GetSiteByCustomDomain(ctx, h.database, requestHostLower(host))
	return err == nil
}

func requestHostLower(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// familyAddr is one family address of a site.
type familyAddr struct {
	Host   string
	Family db.AddressFamily
}

// siteFamilyAddrs is every family address the site answers at, the most
// specific first: the longest site prefix, then the lowest rank, then the
// suffix. Only live families of the site's owner.
func (h *SiteHandler) siteFamilyAddrs(userID, name string) []familyAddr {
	h.families.mu.RLock()
	fams := h.families.byUser[userID]
	h.families.mu.RUnlock()
	var out []familyAddr
	for _, f := range fams {
		label, ok := strings.CutPrefix(name, f.SitePrefix)
		if !ok || !familyLabelOK(f, label) {
			continue
		}
		out = append(out, familyAddr{Host: label + "." + f.Suffix, Family: f})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Family, out[j].Family
		if len(a.SitePrefix) != len(b.SitePrefix) {
			return len(a.SitePrefix) > len(b.SitePrefix)
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		return a.Suffix < b.Suffix
	})
	return out
}

// siteFamilyAddress is the family address handed out for the site: the
// most specific canonical one. ok=false when none.
func (h *SiteHandler) siteFamilyAddress(userID, name string) (string, bool) {
	for _, a := range h.siteFamilyAddrs(userID, name) {
		if a.Family.Canonical {
			return a.Host, true
		}
	}
	return "", false
}

// hasLiveFamilies: some account has a live family (a cheap guard before a
// database read on the hot path).
func (h *SiteHandler) hasLiveFamilies() bool {
	h.families.mu.RLock()
	defer h.families.mu.RUnlock()
	return len(h.families.bySuffix) > 0
}

// siteOwnAddress is the site's main address when it is not its site host or
// person address: its proven own domain (siteOwnDomain), else its most
// specific canonical family address. For a family address, Domain is that
// host and the view counts as verified.
func (h *SiteHandler) siteOwnAddress(ctx context.Context, siteID string) (db.SiteDomainInfo, bool, error) {
	info, has, err := h.siteOwnDomain(ctx, siteID)
	if err != nil || has || !h.hasLiveFamilies() {
		return info, has, err
	}
	_, userID, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.SiteDomainInfo{}, false, nil
	}
	if err != nil {
		return db.SiteDomainInfo{}, false, err
	}
	host, ok := h.siteFamilyAddress(userID, name)
	if !ok {
		return db.SiteDomainInfo{}, false, nil
	}
	return db.SiteDomainInfo{SiteID: siteID, UserID: userID, Name: name, Domain: host, Status: "active",
		VerifiedAt: sql.NullTime{Time: time.Now(), Valid: true}, CertStatus: "live"}, true, nil
}

// isSiteFamilyHost: host is one of the site's family addresses and the site
// does not live on a domain of its own (its family addresses then only
// redirect there).
func (h *SiteHandler) isSiteFamilyHost(ctx context.Context, siteID, host string) bool {
	m, ok, err := h.familySiteForHost(ctx, host)
	if err != nil || !ok || m.Site.ID != siteID {
		return false
	}
	if _, has, err := h.siteOwnDomain(ctx, siteID); err != nil || has {
		return false
	}
	return true
}

// familyRenamedTarget: label under f names an old name of a renamed site
// of f's owner. The site's current address with escapedPath: under the same
// family when its new name still has the prefix, else its main address.
func (h *SiteHandler) familyRenamedTarget(ctx context.Context, f db.AddressFamily, label, escapedPath string) (string, bool) {
	siteID, _, err := db.ResolveOldSiteName(ctx, h.database, f.UserID, f.SitePrefix+label)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("family host: old name %s%s: %v", f.SitePrefix, label, err)
		}
		return "", false
	}
	if _, has, _ := h.siteOwnDomain(ctx, siteID); !has {
		if _, _, name, err := db.GetSiteOwner(ctx, h.database, siteID); err == nil {
			if nl, ok := strings.CutPrefix(name, f.SitePrefix); ok && familyLabelOK(f, nl) {
				return "https://" + nl + "." + f.Suffix + "/" + strings.TrimLeft(escapedPath, "/"), true
			}
		}
	}
	return h.siteAddressFor(ctx, siteID, escapedPath)
}

// FamilyHosts serves <label>.<suffix> for live families, as nginx hands it
// over: /v1/ goes to the API (which binds every lookup to this one site),
// /internal/ to the app's pages (nginx's own rewrites), everything else is
// the site's files through the gate. A site with a domain of its own
// redirects there; an old name of a renamed site redirects to its current
// address. Every other host goes to next.
func (h *SiteHandler) FamilyHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHostName(r)
		f, label, ok := h.liveFamily(host)
		if !ok || strings.HasPrefix(r.URL.Path, "/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		// A custom domain bound here under the family: the exact name wins.
		if _, err := db.GetSiteByCustomDomain(r.Context(), h.database, host); err == nil {
			next.ServeHTTP(w, r)
			return
		}
		query := ""
		if r.URL.RawQuery != "" {
			query = "?" + r.URL.RawQuery
		}
		escaped := r.URL.EscapedPath()
		if escaped == "" {
			escaped = "/"
		}
		m, found, err := h.familySiteForHost(r.Context(), host)
		if err != nil {
			log.Printf("family host %s: %v", host, err)
			h.renderServiceError(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			// Found or not, the API decides: on a family host nothing but
			// the one site the host names resolves.
			api.ServeHTTP(w, r)
			return
		}
		if !found {
			if familyLabelOK(f, label) {
				if target, ok := h.familyRenamedTarget(r.Context(), f, label, escaped); ok {
					w.Header().Set("Cache-Control", "no-store")
					http.Redirect(w, r, target+query, http.StatusFound)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		h.serveFamilyFile(w, r, m, r.URL.Path, escaped)
	})
}

// serveFamilyFile answers one path of the matched site on its family host:
// a site that lives on a domain of its own redirects there (302, so
// disconnecting that domain takes effect at once), anything else is its
// file through the gate (take-down, offline, passcode).
func (h *SiteHandler) serveFamilyFile(w http.ResponseWriter, r *http.Request, m familyMatch, rel, escaped string) {
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	if info, has, err := h.siteOwnDomain(r.Context(), m.Site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+strings.ToLower(info.Domain)+"/"+strings.TrimLeft(escaped, "/")+query, http.StatusFound)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFile(w, r, m.Site.UserID, m.Site.Name, rel, escaped)
}

// familyPageHandler answers /internal/family/{rest...}, where nginx sends a
// family-host request for a site whose folder carries the lives-elsewhere
// marker: the redirect to its own domain (or, when the marker is stale, the
// file itself).
func (h *SiteHandler) familyPageHandler(w http.ResponseWriter, r *http.Request) {
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	m, ok, err := h.familySiteForHost(r.Context(), requestHostName(r))
	if err != nil {
		log.Printf("family page %s: %v", requestHostName(r), err)
		h.renderServiceError(w, r)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveFamilyFile(w, r, m, rest, (&url.URL{Path: rest}).EscapedPath())
}

// FamilyReturnSite resolves a sign-in return_to on a family host to its
// site: the host must name a live site that does not live on a domain of
// its own, and the host must resolve to this server right now (a family
// whose DNS moved away must not receive a sign-in).
func (h *SiteHandler) FamilyReturnSite(ctx context.Context, host string) (string, bool) {
	m, ok, err := h.familySiteForHost(ctx, host)
	if err != nil || !ok {
		return "", false
	}
	if _, has, err := h.siteOwnDomain(ctx, m.Site.ID); err != nil || has {
		return "", false
	}
	if !h.hostPointsHere(ctx, host) {
		return "", false
	}
	return m.Site.ID, true
}

// hostPointsHere: every address host resolves to is this server's, and
// there is at least one.
func (h *SiteHandler) hostPointsHere(ctx context.Context, host string) bool {
	ips, err := lookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	ours := h.serverAddrs(ctx)
	for _, ip := range ips {
		if !ours[ip] {
			return false
		}
	}
	return true
}

// syncLivesElsewhere writes the marker the family servers read: the site
// lives on a domain of its own (custom or claimed), so its family
// addresses redirect there.
func (h *SiteHandler) syncLivesElsewhere(ctx context.Context, siteID, userID, name string) {
	domain := ""
	if info, has, err := h.siteOwnDomain(ctx, siteID); err == nil && has {
		domain = strings.ToLower(info.Domain)
	}
	if err := h.disk.SetLivesElsewhere(userID, name, domain); err != nil {
		log.Printf("address families: lives-elsewhere marker for %s/%s: %v", userID, name, err)
	}
}
