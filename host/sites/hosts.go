package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// LookupHost and LookupTXT read DNS (variables so tests can stand in).
var (
	LookupHost = net.DefaultResolver.LookupHost
	LookupTXT  = net.DefaultResolver.LookupTXT
)

// Hosts answers which site a request host names and which address a site
// has. It holds the instance's address settings, the live address families
// and the product's groups; one is built at startup and shared by pointer.
// The setters run only during startup, before requests are served.
type Hosts struct {
	db         *sql.DB
	disk       Disk
	limits     edition.Limits
	eventSites bool
	hosted     bool
	// eventScoped: notifications are scoped to events, not sites.
	eventScoped bool

	addr          Addressing
	cnameTarget   string
	serverIP      string
	serverIPAlso  []string
	platformZones []string
	familyCertDir string
	domainCertDir string
	families      familyIndex
	groups        GroupHosts
}

// NewHosts starts with every address mode off; the Set methods apply the
// configuration.
func NewHosts(db *sql.DB, disk Disk, ed *edition.Edition, siteDomain, contentHost, cnameTarget, serverIP string) *Hosts {
	return &Hosts{
		db: db, disk: disk, limits: ed.Limits(), eventSites: ed.Storage().EventSites, hosted: ed.Routes().HostedEvents, eventScoped: ed.Notifications().EventScoped,
		addr:        Addressing{SiteDomain: siteDomain, ContentHost: contentHost},
		cnameTarget: cnameTarget, serverIP: serverIP,
	}
}

// SetEdition swaps the edition the hosts answer for (tests rebuild a
// handler as another edition partway through).
func (h *Hosts) SetEdition(ed *edition.Edition) {
	h.limits, h.eventSites, h.hosted, h.eventScoped = ed.Limits(), ed.Storage().EventSites, ed.Routes().HostedEvents, ed.Notifications().EventScoped
	h.addr.HostedEvents = h.addr.TeamSiteFlatHosts && h.hosted
}

// ParseHostMode reads PERSON_HOSTS or SITE_HOSTS; unknown values mean off.
func ParseHostMode(mode string) HostMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "serve":
		return HostsServe
	case "canonical":
		return HostsCanonical
	}
	return HostsOff
}

func (h *Hosts) SetPersonHosts(mode string) { h.addr.PersonHosts = ParseHostMode(mode) }

// SetSiteHosts sets SITE_HOSTS and the certificate hand-off directory.
func (h *Hosts) SetSiteHosts(mode, certDir string) {
	h.addr.SiteHosts = ParseHostMode(mode)
	h.addr.SiteCertDir = strings.TrimSpace(certDir)
}

// SetTeamSiteFlatHosts sets TEAM_SITE_FLAT_HOSTS; it counts only on the
// hosted-events edition.
func (h *Hosts) SetTeamSiteFlatHosts(on bool) {
	h.addr.TeamSiteFlatHosts = on
	h.addr.HostedEvents = on && h.hosted
}

// SetSiteBase sets SITE_BASE_DOMAIN and SITE_BASE_MOVE. A base nested in
// SITE_DOMAIN (or the reverse) is ignored.
func (h *Hosts) SetSiteBase(base, mode string) {
	base = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(base), "."))
	h.addr.BaseMove = BaseMoveOff
	if strings.EqualFold(strings.TrimSpace(mode), "flat") {
		h.addr.BaseMove = BaseMoveFlat
	}
	d := strings.ToLower(strings.TrimSpace(h.addr.SiteDomain))
	if base != "" && d != "" && base != d && (strings.HasSuffix(base, "."+d) || strings.HasSuffix(d, "."+base)) {
		log.Printf("warning: SITE_BASE_DOMAIN %s and SITE_DOMAIN %s are nested; ignoring SITE_BASE_DOMAIN", base, d)
		base = ""
	}
	h.addr.SiteBase = base
}

// SetCustomDomainIPAlso sets the earlier public IPv4s still accepted as
// this server's (CUSTOM_DOMAIN_IP_ALSO).
func (h *Hosts) SetCustomDomainIPAlso(ips []string) { h.serverIPAlso = ips }

// SetPlatformZones sets the platform's other zones (its public host,
// EVENT_DOMAINS): never a custom domain or a family.
func (h *Hosts) SetPlatformZones(zones ...string) {
	h.platformZones = nil
	for _, z := range zones {
		z = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z), "."))
		if z != "" {
			h.platformZones = append(h.platformZones, z)
		}
	}
}

// SetFamilyCertDir sets ADDRESS_FAMILY_CERT_DIR.
func (h *Hosts) SetFamilyCertDir(dir string) { h.familyCertDir = strings.TrimSpace(dir) }

// SetGroupHosts sets the product's groups (nil on a platform-only install).
func (h *Hosts) SetGroupHosts(g GroupHosts) { h.groups = g }

func (h *Hosts) Addressing() Addressing { return h.addr }
func (h *Hosts) Groups() GroupHosts     { return h.groups }
func (h *Hosts) PlatformZones() []string {
	return h.platformZones
}

// ServedBases are the domains whose person, site and claimed-name hosts
// answer: the base first, then SITE_DOMAIN. Just SITE_DOMAIN when not split.
func (h *Hosts) ServedBases() []string {
	d := strings.ToLower(strings.TrimSpace(h.addr.SiteDomain))
	if h.addr.BaseSplit() {
		return []string{h.addr.SiteBase, d}
	}
	if d == "" {
		return nil
	}
	return []string{d}
}

// UserHostLabel is host's one label under any served base.
func (h *Hosts) UserHostLabel(host string) (label, base string, ok bool) {
	for _, b := range h.ServedBases() {
		if l, ok := SubdomainLabel(host, b); ok {
			return l, b, true
		}
	}
	return "", "", false
}

// IsPlatformSubdomain: host is one label under a served base, and not www,
// the content host or the CNAME target.
func (h *Hosts) IsPlatformSubdomain(host string) bool {
	label, _, ok := h.UserHostLabel(host)
	if !ok {
		return false
	}
	return label != "www" && !strings.EqualFold(host, h.addr.ContentHost) && !strings.EqualFold(host, h.cnameTarget)
}

// FlatLabelNotAHandle reports whether label, a single label under a served
// base with flat hosts on, may be read as a flat <site>--<handle> label. It
// fails closed: true only when no account has that handle (an exact handle
// always wins, and a lookup error must never serve one account's page as
// another's).
func (h *Hosts) FlatLabelNotAHandle(ctx context.Context, label string) bool {
	if !strings.Contains(label, "--") {
		return false
	}
	if !HandleAddressable(label) {
		return true
	}
	if h.db == nil {
		return false
	}
	_, err := accounts.GetUserByHandle(ctx, h.db, label)
	return errors.Is(err, sql.ErrNoRows)
}

// flatBase: one-label site hosts <site>--<handle> live under base
// (TEAM_SITE_FLAT_HOSTS under every served base, or the split base).
func (h *Hosts) flatBase(base string) bool {
	return h.addr.TeamSiteFlatHostsOn() || (h.addr.BaseSplit() && strings.EqualFold(base, h.addr.SiteBase))
}

// NameFits checks a person's site name and handle (create, rename,
// restore and handle change) against the one-label address <site>--<handle>:
// it must fit one DNS label whatever style the person has now, since a new
// handle makes anyone flat. Simple Hack's team sites keep their own limit
// (hackteams.MaxTeamSlugLen).
func (h *Hosts) NameFits(site, handle string) error {
	if h == nil || h.eventSites || handle == "" || site == "" {
		return nil
	}
	if err := rules.FlatLabelFits(site, handle); err != nil {
		return &Error{Code: "invalid_name", Message: err.Error()}
	}
	return nil
}

// RequireFlatClean turns SITE_BASE_MOVE=flat into off, with one log line,
// unless the install can hold one-level names. Simple Hack runs it only with
// TEAM_SITE_FLAT_HOSTS. Elsewhere no existing handle (current, alias or
// retired), site name (live or in Recently deleted) or free name may hold
// "--", no site may be named "xn", no free name may be stored under the
// base, and every <site>--<handle> must fit 63 characters. New names keep
// all of that true (rules/sites), so a one-level label always splits into
// the one site it was built from. Run once at startup.
func (h *Hosts) RequireFlatClean(ctx context.Context, teamFlat bool) {
	if !h.addr.BaseSplit() {
		return
	}
	reason := ""
	switch {
	case h.hosted && !teamFlat:
		reason = "Simple Hack runs it only with TEAM_SITE_FLAT_HOSTS=true"
	case h.db != nil:
		handles, err := accounts.CountDoubleHyphenHandles(ctx, h.db)
		var b repo.FlatBlockers
		if err == nil {
			b, err = repo.GetFlatBlockers(ctx, h.db, strings.ToLower(h.addr.SiteDomain), h.addr.SiteBase)
		}
		switch {
		case err != nil:
			reason = "could not check the existing names: " + err.Error()
		case handles+b.SiteNames+b.TooLong+b.FreeNames+b.OnBase > 0:
			reason = fmt.Sprintf(`%d handles (current, alias or retired) holding "--", %d site names holding "--" or named "xn", %d sites whose <site>--<handle> is over 63 characters, %d free names holding "--", %d free names stored under %s; rename or move them, then restart`,
				handles, b.SiteNames, b.TooLong, b.FreeNames, b.OnBase, h.addr.SiteBase)
		}
	}
	if reason != "" {
		log.Printf("SITE_BASE_MOVE=flat refused, running as off: %s", reason)
		h.addr.BaseMove = BaseMoveOff
	}
}

// FlatBases are the served bases with flat site hosts.
func (h *Hosts) FlatBases() []string {
	var out []string
	for _, b := range h.ServedBases() {
		if h.flatBase(b) {
			out = append(out, b)
		}
	}
	return out
}

// SiteHostParts splits <site>.<handle>.<base>, or the single label
// <site>--<handle>.<base> under a flat base, under any served base. The
// split is at the first "--": no new site name holds one.
func (h *Hosts) SiteHostParts(ctx context.Context, host string) (site, handle, base string, ok bool) {
	for _, b := range h.ServedBases() {
		if s, hd, ok := SiteHostLabels(host, b); ok {
			return s, hd, b, true
		}
	}
	for _, b := range h.ServedBases() {
		label, ok := SubdomainLabel(host, b)
		if !ok || !h.flatBase(b) || !h.FlatLabelNotAHandle(ctx, label) {
			continue
		}
		if s, hd, ok := TeamSiteHostLabels(label); ok {
			return s, hd, b, true
		}
	}
	return "", "", "", false
}

// SiteHostLabels splits <site>.<handle>.<siteDomain>; any other shape is
// ok=false.
func SiteHostLabels(host, siteDomain string) (site, handle string, ok bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	if siteDomain == "" || !strings.HasSuffix(host, "."+siteDomain) {
		return "", "", false
	}
	site, handle, ok = strings.Cut(strings.TrimSuffix(host, "."+siteDomain), ".")
	if !ok || site == "" || handle == "" || strings.Contains(handle, ".") {
		return "", "", false
	}
	return site, handle, true
}

// IsSiteHost: host has the shape of a site host.
func (h *Hosts) IsSiteHost(ctx context.Context, host string) bool {
	_, _, _, ok := h.SiteHostParts(ctx, host)
	return ok
}

// HostBase is the served base host is one or two labels under ("" if none).
func (h *Hosts) HostBase(ctx context.Context, host string) string {
	if _, b, ok := h.UserHostLabel(host); ok {
		return b
	}
	if _, _, b, ok := h.SiteHostParts(ctx, host); ok {
		return b
	}
	return ""
}

// BaseTarget is where a request on host goes when host is not served
// under its base (BaseSplit only; ok=false: serve it here). A flat
// person's page under SITE_DOMAIN goes to <handle>.<base>; under the base,
// anything that is not a flat person's page or a flat site host (links
// from the paused 2026-09-28 move, free names, a two-label person's page)
// goes to the same labels under SITE_DOMAIN. A flat site host of a
// two-label person is the site host's own redirect (SiteHostLiveOn).
func (h *Hosts) BaseTarget(ctx context.Context, host string) (string, bool) {
	if !h.addr.BaseSplit() {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	d, base := strings.ToLower(h.addr.SiteDomain), h.addr.SiteBase
	u, _, person := h.personHostUser(ctx, host)
	if label, ok := SubdomainLabel(host, d); ok {
		if person && h.addr.FlatFor(u.Handle.String) {
			return h.addr.PersonHostOn(label, base), true
		}
		return "", false
	}
	if !strings.HasSuffix(host, "."+base) || (person && h.addr.FlatFor(u.Handle.String)) {
		return "", false
	}
	if _, _, b, ok := h.SiteHostParts(ctx, host); ok && b == base && !strings.Contains(strings.TrimSuffix(host, "."+base), ".") {
		return "", false
	}
	return strings.TrimSuffix(host, base) + d, true
}

// IsApexHost: host is the platform's own apex (SITE_DOMAIN, the base, their
// www, or PUBLIC_BASE_URL's host), where no site lives.
func (h *Hosts) IsApexHost(host string) bool {
	host = strings.ToLower(host)
	d := strings.ToLower(h.addr.SiteDomain)
	if host == d || host == "www."+d {
		return true
	}
	if b := strings.ToLower(h.addr.SiteBase); h.addr.BaseSplit() && (host == b || host == "www."+b) {
		return true
	}
	raw := os.Getenv("PUBLIC_BASE_URL")
	if raw == "" {
		raw = "https://simple-host.app"
	}
	if u, err := url.Parse(raw); err == nil {
		if ah := strings.ToLower(u.Hostname()); ah != "" && host == ah {
			return true
		}
	}
	return false
}

// IsSharedHost: host is the content host or the apex, which every site
// shares, so no visitor session or unlock is valid there.
func (h *Hosts) IsSharedHost(host string) bool {
	return (h.addr.ContentHost != "" && strings.EqualFold(host, h.addr.ContentHost)) || h.IsApexHost(host)
}

// PersonHostOwner is the account whose own address host is, when person
// hosts are on. Under a split base a person's host is theirs only under
// their own base (BaseFor).
func (h *Hosts) PersonHostOwner(ctx context.Context, host string) (model.User, bool) {
	u, base, ok := h.personHostUser(ctx, host)
	if !ok || (h.addr.BaseSplit() && !strings.EqualFold(base, h.addr.BaseFor(u.Handle.String))) {
		return model.User{}, false
	}
	return u, true
}

// personHostUser is the account whose handle is host's one label under a
// served base, whichever base that is.
func (h *Hosts) personHostUser(ctx context.Context, host string) (model.User, string, bool) {
	if !h.addr.PersonHostsOn() {
		return model.User{}, "", false
	}
	label, base, ok := h.UserHostLabel(host)
	if !ok || !h.IsPlatformSubdomain(host) || !HandleAddressable(label) {
		return model.User{}, "", false
	}
	u, err := accounts.GetUserByHandle(ctx, h.db, label)
	if err != nil {
		return model.User{}, "", false
	}
	return u, base, true
}

func (h *Hosts) IsPersonHost(ctx context.Context, host string) bool {
	_, ok := h.PersonHostOwner(ctx, host)
	return ok
}

// ServerAddrs is the set of addresses that count as this server: the A
// record handed out for apex domains, the earlier ones still accepted, and
// whatever the CNAME target resolves to.
func (h *Hosts) ServerAddrs(ctx context.Context) map[string]bool {
	ours := map[string]bool{}
	if h.serverIP != "" {
		ours[h.serverIP] = true
	}
	for _, ip := range h.serverIPAlso {
		ours[ip] = true
	}
	if ips, err := LookupHost(ctx, h.cnameTarget); err == nil {
		for _, ip := range ips {
			ours[ip] = true
		}
	}
	return ours
}

// PointsHere: every address host resolves to is this server's, and there is
// at least one.
func (h *Hosts) PointsHere(ctx context.Context, host string) bool {
	ips, err := LookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	ours := h.ServerAddrs(ctx)
	for _, ip := range ips {
		if !ours[ip] {
			return false
		}
	}
	return true
}

// GroupByAccount is the group an account holds; sql.ErrNoRows when none.
func (h *Hosts) GroupByAccount(ctx context.Context, id string) (Group, error) {
	if h.groups == nil {
		return Group{}, sql.ErrNoRows
	}
	return h.groups.ByAccount(ctx, id)
}

// GroupBySlug is the group with this address label; sql.ErrNoRows when none.
func (h *Hosts) GroupBySlug(ctx context.Context, slug string) (Group, error) {
	if h.groups == nil {
		return Group{}, sql.ErrNoRows
	}
	return h.groups.BySlug(ctx, slug)
}

func (h *Hosts) groupHasSite(ctx context.Context, id, name string) (bool, error) {
	if h.groups == nil {
		return false, nil
	}
	return h.groups.HasSite(ctx, id, name)
}
