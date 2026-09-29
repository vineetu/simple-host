package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// The base domain of people's addresses (owner decision 2026-09-28).
//
// SITE_DOMAIN is the app: the dashboard, sign-in, the API, emails, the
// content host and the CNAME target. SITE_BASE_DOMAIN is what every address a
// person or site gets lives under: <handle>.<base>, <site>.<handle>.<base>
// and free <name>.<base> names. On most installs they are the same domain
// (the default), and then every function here reduces to today's single
// domain. The hosted service moves addresses from simple-host.app to
// simple-host.site, so a page on a person's address never shares a
// registrable domain with the app (docs/designs/site-base-domain-move.md).
//
// SITE_BASE_MOVE says how far the move has gone:
//
//   - off: only SITE_DOMAIN addresses exist (the base is ignored).
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

// LabelReserved reports whether label is reserved in the shared namespace
// (for the move-site-base command).
func LabelReserved(label string) bool { return labelReserved(label) }

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

// siteHostParts is siteHostLabels under any served base.
func (h *SiteHandler) siteHostParts(host string) (site, handle, base string, ok bool) {
	for _, b := range h.servedBases() {
		if s, hd, ok := siteHostLabels(host, b); ok {
			return s, hd, b, true
		}
	}
	return "", "", "", false
}

// hostBase is the served base host is one or two labels under ("" if none).
func (h *SiteHandler) hostBase(host string) string {
	if _, b, ok := h.userHostLabel(host); ok {
		return b
	}
	if _, _, b, ok := h.siteHostParts(host); ok {
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
		labels := strings.TrimSuffix(host, "."+p[0])
		parts := strings.Split(labels, ".")
		switch {
		case labels == "" || len(parts) > 2:
			return ""
		case len(parts) == 1 && (parts[0] == "www" || labelReserved(parts[0])):
			return ""
		case len(parts) == 2 && (parts[0] == "" || labelReserved(parts[1])):
			return ""
		}
		return labels + "." + p[1]
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

// siteHostOn is the site host of a site under base.
func (h *SiteHandler) siteHostOn(handle, name, base string) string {
	return strings.ToLower(name) + "." + h.personHostOn(handle, base)
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
func (h *SiteHandler) siteCertReadyOn(handle, base string) bool {
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
// the base. A no-op unless the move is on.
func (h *SiteHandler) SiteBaseHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if label, ok := platformSubdomainLabel(host, base); ok && labelReserved(label) {
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
	labels := strings.TrimSuffix(host, "."+legacy)
	parts := strings.Split(labels, ".")
	switch len(parts) {
	case 1:
		if parts[0] == "" || parts[0] == "www" || labelReserved(parts[0]) {
			return "", 0, false
		}
	case 2:
		if parts[0] == "" || !handleAddressable(parts[1]) {
			return "", 0, false
		}
	default:
		return "", 0, false
	}
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
	if len(parts) == 2 && validSiteName.MatchString(parts[0]) {
		if u, err := db.GetUserByHandle(r.Context(), h.database, parts[1]); err == nil {
			if site, err := db.GetSiteByUser(r.Context(), h.database, u.ID, parts[0]); err == nil {
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
