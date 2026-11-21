package sites

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vsriram/simple-host/host/model"
)

// HostMode is PERSON_HOSTS or SITE_HOSTS: off, serve (the hosts answer, the
// address handed out is still the path URL) or canonical (the hosts are the
// address handed out).
type HostMode int

const (
	HostsOff HostMode = iota
	HostsServe
	HostsCanonical
)

// BaseMove is SITE_BASE_MOVE, the move of every address to SITE_BASE_DOMAIN.
type BaseMove int

const (
	BaseMoveOff BaseMove = iota
	BaseMoveServe
	BaseMoveCanonical
	BaseMoveRedirect
	BaseMovePermanent
)

// contentHostOnlySites keep their path URL as their address, because the page
// depends on something only the content host serves (vineetu/eb2-wait calls
// the root-absolute /eb2-api/* locations nginx has there). On a person host
// they redirect to the path URL. Remove an entry once its dependency moves.
var contentHostOnlySites = map[string]bool{
	"vineetu/eb2-wait": true,
}

// Addressing is the instance's address configuration (SITE_DOMAIN, the
// shared content host, PERSON_HOSTS, SITE_HOSTS, SITE_BASE_DOMAIN and its
// move, the certificate hand-off directories, TEAM_SITE_FLAT_HOSTS) and the
// one owner of the addresses handed out for sites.
type Addressing struct {
	SiteDomain, ContentHost  string
	PersonHosts, SiteHosts   HostMode
	SiteBase                 string
	BaseMove                 BaseMove
	SiteCertDir, BaseCertDir string
	// TeamSiteFlatHosts: team sites live at <team>--<event>.<base>; only
	// on the hosted-events edition (HostedEvents).
	TeamSiteFlatHosts, HostedEvents bool
}

func (a Addressing) PersonHostsOn() bool { return a.PersonHosts != HostsOff && a.SiteDomain != "" }
func (a Addressing) PersonHostsCanonical() bool {
	return a.PersonHosts == HostsCanonical && a.SiteDomain != ""
}
func (a Addressing) SiteHostsOn() bool { return a.SiteHosts != HostsOff && a.PersonHostsOn() }
func (a Addressing) SiteHostsCanonical() bool {
	return a.SiteHosts == HostsCanonical && a.PersonHostsCanonical()
}
func (a Addressing) TeamSiteFlatHostsOn() bool { return a.TeamSiteFlatHosts && a.HostedEvents }

// BaseSplit: addresses are moving to a second base domain.
func (a Addressing) BaseSplit() bool {
	return a.BaseMove != BaseMoveOff && a.SiteBase != "" && a.SiteDomain != "" && !strings.EqualFold(a.SiteBase, a.SiteDomain)
}

// HandoutBase is the domain every address handed out is under.
func (a Addressing) HandoutBase() string {
	if a.BaseSplit() && a.BaseMove >= BaseMoveCanonical {
		return a.SiteBase
	}
	return strings.ToLower(a.SiteDomain)
}

// PersonHostOn is a person's own host under base: <handle>.<base>.
func (a Addressing) PersonHostOn(handle, base string) string {
	return strings.ToLower(handle) + "." + strings.ToLower(base)
}

// PersonAddressFor reports whether this site is served at its owner's person
// address (the mode is on, the handle can be a host, and the site is not
// pinned to the content host).
func (a Addressing) PersonAddressFor(handle, name string) bool {
	return a.PersonHostsOn() && HandleAddressable(handle) && !contentHostOnlySites[handle+"/"+name]
}

// SiteHostOn is the site host of a site under base: <name>.<handle>.<base>,
// or the flat <name>--<handle>.<base> label under TEAM_SITE_FLAT_HOSTS.
func (a Addressing) SiteHostOn(handle, name, base string) string {
	return TeamSiteHost(handle, name, base, a.TeamSiteFlatHostsOn())
}

// CertDirFor is the certificate hand-off directory for base.
func (a Addressing) CertDirFor(base string) string {
	if a.BaseSplit() && strings.EqualFold(base, a.SiteBase) {
		return a.BaseCertDir
	}
	return a.SiteCertDir
}

// SiteCertReadyOn reports whether *.<handle>.<base> is served with a valid
// certificate: the issuer's ready marker exists. Without a hand-off directory
// every person counts as ready, and so does every event under
// TEAM_SITE_FLAT_HOSTS (team sites live under the instance's own wildcard).
func (a Addressing) SiteCertReadyOn(handle, base string) bool {
	if a.TeamSiteFlatHostsOn() {
		return true
	}
	dir := a.CertDirFor(base)
	if dir == "" {
		return true
	}
	if !HandleAddressable(handle) {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "ready", strings.ToLower(handle)))
	return err == nil && st.Mode().IsRegular()
}

// SiteHostLiveOn: the site is served at its site host under base.
func (a Addressing) SiteHostLiveOn(handle, name, base string) bool {
	return SiteHostAddress(handle, name, base, a.SiteHostsOn(), a.PersonAddressFor(handle, name), a.SiteCertReadyOn(handle, base)) != ""
}

// SiteHostCanonicalOn: the site host under base is this site's address.
func (a Addressing) SiteHostCanonicalOn(handle, name, base string) bool {
	return a.SiteHostsCanonical() && a.SiteHostLiveOn(handle, name, base)
}

// SiteURL is the address handed out for a site: its site host or person
// address in canonical mode, else the path URL on the content host. "" without
// a handle.
func (a Addressing) SiteURL(handle, name string) string {
	if handle == "" {
		return ""
	}
	base := a.HandoutBase()
	if a.SiteHostCanonicalOn(handle, name, base) {
		return "https://" + a.SiteHostOn(handle, name, base) + "/"
	}
	if a.PersonHostsCanonical() && a.PersonAddressFor(handle, name) {
		return "https://" + a.PersonHostOn(handle, base) + "/" + name + "/"
	}
	host := a.ContentHost
	if host == "" {
		host = "sites." + a.SiteDomain
	}
	return "https://" + host + "/" + handle + "/" + name + "/"
}

// KnownBases are SITE_DOMAIN and SITE_BASE_DOMAIN whether or not the move is
// on: for refusing our own zones as custom domains and classifying stored
// names, never for serving.
func (a Addressing) KnownBases() []string {
	d := strings.ToLower(strings.TrimSpace(a.SiteDomain))
	if a.SiteBase != "" && a.SiteBase != d {
		return []string{d, a.SiteBase}
	}
	return []string{d}
}

// IsFreeName: d is one label under SITE_DOMAIN or SITE_BASE_DOMAIN (a
// claimed free name, whichever domain it was stored under).
func (a Addressing) IsFreeName(d string) bool {
	for _, b := range a.KnownBases() {
		if _, ok := SubdomainLabel(d, b); ok {
			return true
		}
	}
	return false
}

// SubdomainLabel is host's one label under domain, ok=false when host is
// not exactly one label under it.
func SubdomainLabel(host, domain string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || !strings.HasSuffix(host, "."+domain) {
		return "", false
	}
	label := strings.TrimSuffix(host, "."+domain)
	if label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

// SharedOrigin: every site is served on one shared address (no site hosts,
// no person hosts), so no site has a browser origin of its own.
func (a Addressing) SharedOrigin() bool { return !a.SiteHostsOn() && !a.PersonHostsOn() }

// ContentBaseURL is the content host's origin.
func (a Addressing) ContentBaseURL() string {
	host := a.ContentHost
	if host == "" {
		host = "sites." + a.SiteDomain
	}
	return "https://" + host
}

// PersonPageURL is the address of an account's public page. "" without a
// handle.
func (a Addressing) PersonPageURL(handle string) string {
	if handle == "" {
		return ""
	}
	if a.PersonHostsCanonical() && HandleAddressable(handle) {
		return "https://" + a.PersonHostOn(handle, a.HandoutBase()) + "/"
	}
	return a.ContentBaseURL() + "/" + handle
}

// SiteAddressWithPath is the site's current address with an escaped path
// (starting with "/") appended: its site host when that is canonical, else
// its person-path address.
func (a Addressing) SiteAddressWithPath(handle, name, escapedPath string) string {
	return a.SiteAddressWithPathOn(handle, name, escapedPath, a.HandoutBase())
}

// SiteAddressWithPathOn is SiteAddressWithPath under base.
func (a Addressing) SiteAddressWithPathOn(handle, name, escapedPath, base string) string {
	if a.SiteHostCanonicalOn(handle, name, base) {
		return "https://" + a.SiteHostOn(handle, name, base) + "/" + strings.TrimLeft(escapedPath, "/")
	}
	return "https://" + a.PersonHostOn(handle, base) + "/" + name + "/" + strings.TrimLeft(escapedPath, "/")
}

// SiteURLFor is SiteURL for a site row, falling back to the stored address
// for the rare site whose owner has no handle.
func (a Addressing) SiteURLFor(site model.Site) string {
	if u := a.SiteURL(site.OwnerHandle, site.Name); u != "" {
		return u
	}
	return site.SiteURL
}

// TeamSiteURL is a team site's address under its event.
func (a Addressing) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + a.SiteHostOn(eventSlug, teamSlug, a.HandoutBase()) + "/"
}

// ContentHostOnly: the site keeps its path URL on the content host.
func ContentHostOnly(handle, name string) bool { return contentHostOnlySites[handle+"/"+name] }

// PersonPathAddressOn is https://<handle>.<base>/<site>/.
func (a Addressing) PersonPathAddressOn(handle, name, base string) string {
	return "https://" + a.PersonHostOn(handle, base) + "/" + name + "/"
}
