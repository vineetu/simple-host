package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	appsites "github.com/vsriram/simple-host/internal/app/sites"
)

// The base domain of people's addresses (owner decision 2026-09-28).
//
// SITE_DOMAIN is the app: the dashboard, sign-in, the API, emails, the
// content host and the CNAME target. SITE_BASE_DOMAIN is what every address a
// person or site gets lives under: <handle>.<base>, <site>.<handle>.<base>
// and free <name>.<base> names. On most installs they are the same domain
// (the default), and then every function here reduces to today's single
// domain. The hosted move is paused (owner decision 2026-10-09):
// simple-host.app is the only address handed out.
//
// SITE_BASE_MOVE says how far the move has gone:
//
//   - off: only SITE_DOMAIN serves; a distinct configured base 301s back
//     to the same address on SITE_DOMAIN, keeping path and query.
//   - serve: both domains answer; addresses handed out stay on SITE_DOMAIN.
//   - canonical: base addresses are handed out; SITE_DOMAIN ones still answer.
//   - redirect: SITE_DOMAIN addresses 302 to the same labels, path and query
//     under the base (never /v1/, which old pages keep calling).
//   - permanent: the same with 301.
//
// The label namespace is one across both domains (the same handle, site and
// claimed names), so the redirect is a pure suffix swap and can be permanent.

type baseMoveMode int

const (
	baseMoveOff baseMoveMode = iota
	baseMoveServe
	baseMoveCanonical
	baseMoveRedirect
	baseMovePermanent
)

// SetSiteBase sets SITE_BASE_DOMAIN, SITE_BASE_MOVE and SITE_BASE_CERT_DIR.
// Unknown modes mean off. A base nested in SITE_DOMAIN (or the reverse) is
// refused: its hosts would read as two different addresses.
func (h *SiteHandler) SetSiteBase(base, mode, certDir string) {
	h.siteBase = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(base), "."))
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "serve":
		h.baseMove = baseMoveServe
	case "canonical":
		h.baseMove = baseMoveCanonical
	case "redirect":
		h.baseMove = baseMoveRedirect
	case "permanent":
		h.baseMove = baseMovePermanent
	default:
		h.baseMove = baseMoveOff
	}
	h.baseCertDir = strings.TrimSpace(certDir)
	d := strings.ToLower(strings.TrimSpace(h.siteDomain))
	if h.siteBase != "" && d != "" && h.siteBase != d &&
		(strings.HasSuffix(h.siteBase, "."+d) || strings.HasSuffix(d, "."+h.siteBase)) {
		log.Printf("warning: SITE_BASE_DOMAIN %s and SITE_DOMAIN %s are nested; ignoring SITE_BASE_DOMAIN", h.siteBase, d)
		h.siteBase = ""
	}
	if h.baseSplit() && h.baseCertDir == "" && h.siteCertDir != "" {
		log.Printf("warning: SITE_BASE_CERT_DIR is empty while SITE_CERT_DIR is set: every person counts as having a certificate under %s", h.siteBase)
	}
}

// baseSplit: addresses live under a base other than SITE_DOMAIN and the move
// is on. False on every single-domain install.
func (h *SiteHandler) baseSplit() bool {
	return h.baseMove != baseMoveOff && h.siteBase != "" && h.siteDomain != "" &&
		!strings.EqualFold(h.siteBase, h.siteDomain)
}

// handoutBase is the domain every address handed out is under.
func (h *SiteHandler) handoutBase() string {
	if h.baseSplit() && h.baseMove >= baseMoveCanonical {
		return h.siteBase
	}
	return strings.ToLower(h.siteDomain)
}

// HandoutBase is handoutBase for main (the namespace and analytics wiring).
func (h *SiteHandler) HandoutBase() string { return h.handoutBase() }

// SameUserHost is sameUserHost for the OAuth return_to check.
func (h *SiteHandler) SameUserHost(a, b string) bool { return h.sameUserHost(a, b) }

// knownBases are SITE_DOMAIN and SITE_BASE_DOMAIN whether or not the move is
// on: for refusing our own zones as custom domains and classifying stored
// names, never for serving.
func (h *SiteHandler) knownBases() []string {
	d := strings.ToLower(strings.TrimSpace(h.siteDomain))
	if h.siteBase != "" && h.siteBase != d {
		return []string{d, h.siteBase}
	}
	return []string{d}
}

// isFreeName: d is one label under SITE_DOMAIN or SITE_BASE_DOMAIN (a
// claimed free name, whichever domain it was stored under).
func (h *SiteHandler) isFreeName(d string) bool {
	for _, b := range h.knownBases() {
		if _, ok := platformSubdomainLabel(d, b); ok {
			return true
		}
	}
	return false
}

// ServedBases is servedBases for main.
func (h *SiteHandler) ServedBases() []string { return h.servedBases() }

// legacyRedirects: SITE_DOMAIN addresses redirect to the base.
func (h *SiteHandler) legacyRedirects() bool {
	return h.baseSplit() && h.baseMove >= baseMoveRedirect
}

// servedBases are the domains whose person, site and claimed-name hosts
// answer: the base first, then SITE_DOMAIN. Just SITE_DOMAIN when not split.
func (h *SiteHandler) servedBases() []string {
	d := strings.ToLower(strings.TrimSpace(h.siteDomain))
	if h.baseSplit() {
		return []string{h.siteBase, d}
	}
	if d == "" {
		return nil
	}
	return []string{d}
}

// isServedBase: b is one of servedBases.
func (h *SiteHandler) isServedBase(b string) bool {
	for _, s := range h.servedBases() {
		if strings.EqualFold(s, b) {
			return true
		}
	}
	return false
}

// userHostLabel is platformSubdomainLabel under any served base.
func (h *SiteHandler) userHostLabel(host string) (label, base string, ok bool) {
	for _, b := range h.servedBases() {
		if l, ok := platformSubdomainLabel(host, b); ok {
			return l, b, true
		}
	}
	return "", "", false
}

// flatLabelConfirmedNotAHandle reports whether label — a single DNS label
// under a served base, with flat hosts on — may safely be read as a flat
// <team>--<event> label rather than some account's own host. False
// whenever that cannot be ruled out: no "--" at all (never a flat label),
// or label is itself a real account handle, or the lookup to check that
// could not be completed.
//
// Shared by siteHostParts (routing) and hackTLSHostExists (the on-demand
// TLS allowlist), so both apply the exact-handle-match-wins rule the same
// way: a single label that is itself a real, addressable account handle —
// an event created before "--" was forbidden in slugs, or any future
// coincidence — is always that person/event's own host, never read as a
// flat team label. Checked with one GetUserByHandle lookup, only for a
// label that actually contains "--", so every other host costs nothing
// extra. Fails closed: true only on a confirmed "no such handle"
// (sql.ErrNoRows); a nil database or any other lookup error returns
// false — a database hiccup must never risk serving one account's page as
// another's.
func (h *SiteHandler) flatLabelConfirmedNotAHandle(ctx context.Context, label string) bool {
	if !strings.Contains(label, "--") {
		return false
	}
	if !handleAddressable(label) {
		return true // too long, or otherwise shaped, to ever be a real handle
	}
	if h.database == nil {
		return false // fail closed: no way to rule out a real handle
	}
	_, err := h.storeAccounts().GetUserByHandle(ctx, label)
	if err == nil {
		return false // a real handle: its own host, never a team label
	}
	return errors.Is(err, sql.ErrNoRows) // fail closed on any other error
}

// siteHostParts is siteHostLabels under any served base, plus — only when
// teamSiteFlatHostsOn() — the flat single-label <team>--<event> form. Every
// caller (siteHostSite, SiteHosts, isSiteHostName, hostBase) resolves either
// shape through the exact same lookup, so a flat team-site host is routed
// identically to today's two-level one. With the setting off this is
// unchanged: only siteHostLabels ever runs, so a "--" label is never read as
// a team site. Exact handle match wins (flatLabelConfirmedNotAHandle) before
// the flat split, so an existing event never loses its own origin to a
// team's.
func (h *SiteHandler) siteHostParts(ctx context.Context, host string) (site, handle, base string, ok bool) {
	for _, b := range h.servedBases() {
		if s, hd, ok := siteHostLabels(host, b); ok {
			return s, hd, b, true
		}
	}
	if h.teamSiteFlatHostsOn() {
		for _, b := range h.servedBases() {
			label, ok := platformSubdomainLabel(host, b)
			if !ok || !h.flatLabelConfirmedNotAHandle(ctx, label) {
				continue
			}
			if s, hd, ok := appsites.TeamSiteHostLabels(label); ok {
				return s, hd, b, true
			}
		}
	}
	return "", "", "", false
}

// hostBase is the served base host is one or two labels under ("" if none).
func (h *SiteHandler) hostBase(ctx context.Context, host string) string {
	if _, b, ok := h.userHostLabel(host); ok {
		return b
	}
	if _, _, b, ok := h.siteHostParts(ctx, host); ok {
		return b
	}
	return ""
}

// twinHost is a person, site or claimed-name host under one base written
// under the other (clay.simple-host.app <-> clay.simple-host.site): the same
// address, since the label namespace is shared. "" when not split, or for
// any other host (the apex, www, a reserved name, a custom domain).
func (h *SiteHandler) twinHost(host string) string {
	if !h.baseSplit() {
		return ""
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	d := strings.ToLower(h.siteDomain)
	for _, p := range [][2]string{{h.siteBase, d}, {d, h.siteBase}} {
		if !strings.HasSuffix(host, "."+p[0]) {
			continue
		}
		return appsites.TwinHost(host, p[0], p[1])
	}
	return ""
}

// sameUserHost: a and b are the same address, allowing for the base swap.
func (h *SiteHandler) sameUserHost(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	t := h.twinHost(a)
	return t != "" && strings.EqualFold(t, b)
}

// personHostOn is the person host of handle under base.
func (h *SiteHandler) personHostOn(handle, base string) string {
	return strings.ToLower(handle) + "." + strings.ToLower(base)
}

// siteHostOn is the site host of a site under base: <name>.<handle>.<base>,
// or, under teamSiteFlatHostsOn(), the flat <name>--<handle>.<base> single
// label (appsites.TeamSiteHost, the one function that knows the format).
func (h *SiteHandler) siteHostOn(handle, name, base string) string {
	return appsites.TeamSiteHost(handle, name, base, h.teamSiteFlatHostsOn())
}

// certDirFor is the certificate hand-off directory for base.
func (h *SiteHandler) certDirFor(base string) string {
	if h.baseSplit() && strings.EqualFold(base, h.siteBase) {
		return h.baseCertDir
	}
	return h.siteCertDir
}

// siteCertReadyOn reports whether *.<handle>.<base> is served with a valid
// certificate. Without a hand-off directory every person counts as ready.
//
// Under TEAM_SITE_FLAT_HOSTS every handle here is a hosted event's own
// (team sites are the only site hosts on that edition), and team sites live
// under the instance's own *.<base> wildcard instead of a per-event one —
// RequestSiteCertContext (sitehost.go) never requests, or waits on, a
// per-event marker, so every event counts as ready.
func (h *SiteHandler) siteCertReadyOn(handle, base string) bool {
	if h.teamSiteFlatHostsOn() {
		return true
	}
	dir := h.certDirFor(base)
	if dir == "" {
		return true
	}
	if !handleAddressable(handle) {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "ready", strings.ToLower(handle)))
	return err == nil && st.Mode().IsRegular()
}

// certRequestBases are the bases a person's certificate is requested under:
// SITE_DOMAIN until the base is handed out (its certificates then only
// renew), and the base from serve on.
func (h *SiteHandler) certRequestBases() []string {
	var out []string
	if h.siteCertDir != "" && !(h.baseSplit() && h.baseMove >= baseMoveCanonical) {
		out = append(out, strings.ToLower(h.siteDomain))
	}
	if h.baseSplit() && h.baseCertDir != "" {
		out = append(out, h.siteBase)
	}
	return out
}

// SiteBaseHosts answers what only the move adds, in front of everything else:
// the base's apex, www and reserved names go to the app, and in redirect and
// permanent mode an address under SITE_DOMAIN goes to the same address under
// the base. When off, a distinct configured base redirects back to SITE_DOMAIN.
func (h *SiteHandler) SiteBaseHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A paused move keeps already-shared base links working, including API
		// paths, without serving content or requesting certificates there.
		if h.baseMove == baseMoveOff && h.siteBase != "" && h.siteDomain != "" &&
			!strings.EqualFold(h.siteBase, h.siteDomain) {
			host := strings.TrimSuffix(requestHostName(r), ".")
			if host == h.siteBase || strings.HasSuffix(host, "."+h.siteBase) {
				target := strings.TrimSuffix(host, h.siteBase) + strings.ToLower(h.siteDomain)
				http.Redirect(w, r, "https://"+target+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
		}
		if !h.baseSplit() {
			next.ServeHTTP(w, r)
			return
		}
		host := requestHostName(r)
		base := h.siteBase
		// The base is only ever people's addresses: its apex, www and the
		// platform's own names are the app's.
		if host == base || host == "www."+base {
			http.Redirect(w, r, "https://"+strings.ToLower(h.siteDomain)+"/", http.StatusMovedPermanently)
			return
		}
		if appsites.PlatformHostTarget(host, base, strings.ToLower(h.siteDomain)) != "" {
			http.Redirect(w, r, "https://"+strings.ToLower(h.siteDomain)+"/", http.StatusMovedPermanently)
			return
		}
		if h.legacyRedirects() {
			if target, status, ok := h.legacyBaseTarget(r); ok {
				if status == http.StatusFound {
					w.Header().Set("Cache-Control", "no-store")
				}
				http.Redirect(w, r, target, status)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// legacyBaseTarget is where a request on an address under SITE_DOMAIN goes
// under the base: the same labels, escaped path and query, 302 in redirect
// mode and 301 in permanent. ok=false for anything that stays: /v1/ (browsers
// do not follow a redirect on a CORS preflight, and a 301 turns a POST into a
// GET), the content host, the CNAME target, www, reserved names and every
// other shape of host. A site host whose owner has no certificate under the
// base yet goes to its person path there instead, always with a 302.
func (h *SiteHandler) legacyBaseTarget(r *http.Request) (string, int, bool) {
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/internal/") {
		return "", 0, false
	}
	host := strings.TrimSuffix(requestHostName(r), ".")
	legacy := strings.ToLower(h.siteDomain)
	if !strings.HasSuffix(host, "."+legacy) || strings.EqualFold(host, h.contentHost) ||
		strings.EqualFold(host, strings.TrimSuffix(h.cnameTarget, ".")) {
		return "", 0, false
	}
	parts, ok := appsites.LegacyBaseLabels(host, legacy)
	if !ok {
		return "", 0, false
	}
	labels := strings.Join(parts, ".")
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, "/") {
		escaped = "/" + escaped
	}
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	// A site whose main address is not a Simple Host one (its own domain,
	// or its address family) goes straight there: one hop, and a 302, since
	// that address can change.
	if len(parts) == 2 && h.database != nil {
		if u, err := h.sitesSvc().HostOwner(r.Context(), parts[1], parts[0]); err == nil {
			if site, err := h.sitesSvc().AddressedSite(r.Context(), u.ID, parts[0]); err == nil {
				if own, has, err := h.siteOwnAddress(r.Context(), site.ID); err == nil && has {
					return "https://" + strings.ToLower(own.Domain) + escaped + query, http.StatusFound, true
				}
			}
		}
	}
	if len(parts) == 2 && !h.siteCertReadyOn(parts[1], h.siteBase) {
		return "https://" + h.personHostOn(parts[1], h.siteBase) + "/" + parts[0] + escaped + query, http.StatusFound, true
	}
	status := http.StatusFound
	if h.baseMove == baseMovePermanent {
		status = http.StatusMovedPermanently
	}
	return "https://" + labels + "." + h.siteBase + escaped + query, status, true
}

// moveStatus is the status of a redirect from an old address to a person
// address: 302 until the move is permanent, then 301 (the content host's
// person-address redirects; docs/designs/per-person-subdomains-nginx.md step 3).
func (h *SiteHandler) moveStatus() int {
	if h.baseSplit() && h.baseMove == baseMovePermanent {
		return http.StatusMovedPermanently
	}
	return http.StatusFound
}
