package sites

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// Address families.
//
// An account connects *.<suffix> once (model.AddressFamily). Every site of
// that account named <prefix><label> then answers at <label>.<suffix>.
// Nothing else of the account, and nothing of any other account, is ever
// reachable there: the host alone names the account and the site. A family
// serves only once it is verified AND the issuer has written its ready
// marker ($ADDRESS_FAMILY_CERT_DIR/ready/<suffix>) with the prefix the
// database has now. Ranking of a site's addresses: own domain > the most
// specific canonical family > the site host.

// familyIndex is the live families requests are routed by.
type familyIndex struct {
	mu       sync.RWMutex
	bySuffix map[string]model.AddressFamily
	byUser   map[string][]model.AddressFamily
	// known is every proven family's suffix, live or not: a host under one
	// is never handed to the platform's pages. An unproven family is not
	// known: anyone may connect any name, and must not blank its hosts.
	known map[string]bool
	// released is every proven suffix let go since this process started (a
	// lapsed family's wildcard may still point here): known too.
	released map[string]bool
}

// FamiliesOn: families may exist and be served here.
func (h *Hosts) FamiliesOn() bool { return h.limits.AddressFamilies && h.familyCertDir != "" }

func (h *Hosts) FamilyCertDir() string { return h.familyCertDir }

// FamilyReadyPrefix reads the issuer's ready marker for suffix: whether it
// exists, and the site prefix the served configuration maps labels with.
func (h *Hosts) FamilyReadyPrefix(suffix string) (string, bool) {
	if h.familyCertDir == "" || !ValidFamilyKey(suffix) {
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

// ValidFamilyKey: a suffix that is safe as a file name.
func ValidFamilyKey(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/\\") && !strings.HasPrefix(s, ".") && !strings.Contains(s, "..")
}

// FamilyIsLive: verified, wildcard certificate named, and the issuer serves
// it with the prefix the database has now.
func (h *Hosts) FamilyIsLive(f model.AddressFamily) bool {
	if !f.VerifiedAt.Valid || f.CertMode != "wildcard" || f.CertName == "" {
		return false
	}
	p, ok := h.FamilyReadyPrefix(f.Suffix)
	return ok && p == f.SitePrefix
}

// RefreshFamilies reloads the live families. The redirect markers of every
// account whose live families changed follow.
func (h *Hosts) RefreshFamilies(ctx context.Context) {
	bySuffix := map[string]model.AddressFamily{}
	byUser := map[string][]model.AddressFamily{}
	known := map[string]bool{}
	if h.FamiliesOn() {
		list, err := repo.ListAllFamilies(ctx, h.db)
		if err != nil {
			ReportWorkerFailure(ctx, "refresh address families", err)
			return
		}
		for _, f := range list {
			if f.VerifiedAt.Valid || f.ProofExempt {
				known[f.Suffix] = true
			}
			if h.FamilyIsLive(f) {
				bySuffix[f.Suffix] = f
				byUser[f.UserID] = append(byUser[f.UserID], f)
			}
		}
	}
	h.families.mu.Lock()
	for s := range h.families.released {
		known[s] = true
	}
	old := h.families.byUser
	h.families.bySuffix, h.families.byUser, h.families.known = bySuffix, byUser, known
	h.families.mu.Unlock()
	changed := map[string]bool{}
	for u, fs := range byUser {
		if familySignature(old[u]) != familySignature(fs) {
			changed[u] = true
		}
	}
	for u, fs := range old {
		if familySignature(byUser[u]) != familySignature(fs) {
			changed[u] = true
		}
	}
	if len(changed) > 0 {
		ReportWorkerProgress(ctx)
	}
	for u := range changed {
		h.SyncUserRedirects(ctx, u)
	}
}

func familySignature(fs []model.AddressFamily) string {
	var b strings.Builder
	for _, f := range fs {
		canonical := "0"
		if f.Canonical {
			canonical = "1"
		}
		b.WriteString(f.Suffix + "|" + f.SitePrefix + "|" + canonical + "|" + strconv.Itoa(f.Rank) + ";")
	}
	return b.String()
}

// ReleaseFamily keeps a let-go suffix known, from the next refresh until
// restart (its wildcard may still point here).
func (h *Hosts) ReleaseFamily(suffix string) {
	h.families.mu.Lock()
	defer h.families.mu.Unlock()
	if h.families.released == nil {
		h.families.released = map[string]bool{}
	}
	h.families.released[suffix] = true
}

// LiveFamily is the live family host is directly under, and its first
// label. Purely the index: no database.
func (h *Hosts) LiveFamily(host string) (model.AddressFamily, string, bool) {
	label, suffix, ok := strings.Cut(normalHost(host), ".")
	if !ok || label == "" {
		return model.AddressFamily{}, "", false
	}
	h.families.mu.RLock()
	f, ok := h.families.bySuffix[suffix]
	h.families.mu.RUnlock()
	if !ok {
		return model.AddressFamily{}, "", false
	}
	return f, label, true
}

// FamilyServed: f is live in the index requests are routed by.
func (h *Hosts) FamilyServed(f model.AddressFamily) bool {
	h.families.mu.RLock()
	g, ok := h.families.bySuffix[f.Suffix]
	h.families.mu.RUnlock()
	return ok && g.ID == f.ID && g.SitePrefix == f.SitePrefix
}

// KnownFamilyHost: host is <label>.<suffix> of a proven family, live or not.
func (h *Hosts) KnownFamilyHost(host string) bool {
	_, suffix, ok := strings.Cut(normalHost(host), ".")
	if !ok {
		return false
	}
	h.families.mu.RLock()
	defer h.families.mu.RUnlock()
	return h.families.known[suffix]
}

// IsFamilyHost: host is <label>.<a live family's suffix>, whatever the label.
func (h *Hosts) IsFamilyHost(host string) bool {
	_, _, ok := h.LiveFamily(host)
	return ok
}

// HasLiveFamilies: some account has a live family (a cheap guard before a
// database read on the hot path).
func (h *Hosts) HasLiveFamilies() bool {
	h.families.mu.RLock()
	defer h.families.mu.RUnlock()
	return len(h.families.bySuffix) > 0
}

// FamilyMatch is what a family host names.
type FamilyMatch struct {
	Family model.AddressFamily
	Label  string
	Site   model.Site
}

// Host is the family address of the matched site.
func (m FamilyMatch) Host() string { return m.Label + "." + m.Family.Suffix }

// FamilySiteForHost resolves a family host to the family owner's live site
// named <prefix><label>. A host that is also a custom domain bound here is
// that domain's (the exact name wins).
func (h *Hosts) FamilySiteForHost(ctx context.Context, host string) (FamilyMatch, bool, error) {
	f, label, ok := h.LiveFamily(host)
	if !ok || !FamilyLabelOK(f, label, h.limits) {
		return FamilyMatch{}, false, nil
	}
	if _, err := h.siteByDomain(ctx, normalHost(host)); err == nil {
		return FamilyMatch{}, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return FamilyMatch{}, false, err
	}
	site, err := repo.GetSiteByUser(ctx, h.db, f.UserID, f.SitePrefix+label)
	if errors.Is(err, sql.ErrNoRows) {
		return FamilyMatch{}, false, nil
	}
	if err != nil {
		return FamilyMatch{}, false, err
	}
	return FamilyMatch{Family: f, Label: label, Site: site}, true, nil
}

// FamilyAddr is one family address of a site.
type FamilyAddr struct {
	Host   string
	Family model.AddressFamily
}

// SiteFamilyAddrs is every family address the site answers at, the most
// specific first: the longest site prefix, then the lowest rank, then the
// suffix.
func (h *Hosts) SiteFamilyAddrs(userID, name string) []FamilyAddr {
	h.families.mu.RLock()
	fams := h.families.byUser[userID]
	h.families.mu.RUnlock()
	var out []FamilyAddr
	for _, f := range fams {
		label, ok := strings.CutPrefix(name, f.SitePrefix)
		if ok && FamilyLabelOK(f, label, h.limits) {
			out = append(out, FamilyAddr{Host: label + "." + f.Suffix, Family: f})
		}
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

// SiteFamilyAddress is the family address handed out for the site: the most
// specific canonical one.
func (h *Hosts) SiteFamilyAddress(userID, name string) (string, bool) {
	for _, a := range h.SiteFamilyAddrs(userID, name) {
		if a.Family.Canonical {
			return a.Host, true
		}
	}
	return "", false
}

// IsBoundDomain: host is a custom domain (or earlier address) bound here.
func (h *Hosts) IsBoundDomain(ctx context.Context, host string) bool {
	_, err := h.siteByDomain(ctx, normalHost(host))
	return err == nil
}

func (h *Hosts) siteByDomain(ctx context.Context, host string) (model.SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, h.db, host, accounts.HostForms(host))
}

func normalHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}
