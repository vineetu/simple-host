package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// A free <name>.<SITE_DOMAIN> address (owner decision 2026-09-24).
//
// A site can claim one single-label name under the platform domain, e.g.
// clayandkiln.simple-host.app, through the ordinary custom-domain binding (the
// same sites.custom_domain column, the same POST /v1/sites/{site}/domain and
// connect_domain tool). Because the platform owns the zone, the wildcard
// certificate and the wildcard DNS record, there is nothing for the person to
// prove: the binding is verified the moment it is made, and it is first come,
// first served. Once bound, the host behaves exactly like a custom domain — the
// site is served at its root, /v1 is same-origin, visitors sign in there with a
// host-only __Host- cookie — and so it counts as the site's own domain for
// private collections. Names nobody has claimed keep the legacy 301 to the path
// URL (LegacyHostRedirect).

// reservedSubdomainLabels is the single source of truth for names under the
// platform domain that no site may claim. It must contain every label nginx
// answers itself with an exact server_name (those never reach the app, so a
// claim there would be a binding that can never serve) plus the names the
// platform uses or may use. scripts/check-reserved-subdomains.sh compares it
// against the live nginx config where that is readable.
var reservedSubdomainLabels = []string{
	// Platform hosts.
	"www", "sites", "api", "admin", "mail", "cname", "app", "apex",
	"auth", "login", "oauth", "mcp", "connect", "dashboard", "console",
	"static", "assets", "cdn", "docs", "status", "help", "support", "blog",
	"smtp", "imap", "pop", "ftp", "ns", "ns1", "ns2", "mx", "email",
	"internal", "v1", "skills", "plugin", "install", "healthz", "readyz",
	"hack", "events", "test", "staging", "dev", "localhost",
	// Hand-configured nginx exact server_names on the live box (and the
	// wildcard *.lab block's parent name).
	"sf-fog", "gods-eye", "paragliding-beginners-map", "lab",
}

// reservedSubdomainSet is every reserved name in the one shared namespace
// (owner decision 2026-09-25): the labels above plus every reserved handle,
// since a handle is also an address (<handle>.<SITE_DOMAIN>) and a claimed
// name is also a path segment on the legacy content host.
var reservedSubdomainSet = func() map[string]bool {
	m := make(map[string]bool, len(reservedSubdomainLabels)+len(reservedHandles))
	for _, l := range reservedSubdomainLabels {
		m[l] = true
	}
	for l := range reservedHandles {
		m[l] = true
	}
	return m
}()

// labelReserved reports whether a name is reserved in the shared namespace:
// no account may take it as a handle and no site may claim it.
func labelReserved(label string) bool {
	return reservedSubdomainSet[strings.ToLower(label)]
}

// reservedNewNames (owner decision 2026-09-27) are names that read as the
// platform, its operator or a sensitive function, so a stranger holding one
// could pass for Simple Host. They are refused to NEW claims only: a new or
// changed handle, a newly claimed <name>.<SITE_DOMAIN>, and (the subset in
// reservedNewSiteNames) a new or renamed site. Accounts and sites that
// already hold one keep it and keep working (it is not a serving check), so
// this list never goes into reservedSubdomainSet. docs/advanced/
// server-and-addresses.md lists it; keep the two together.
var reservedNewNames = []string{
	"admin", "administrator", "root", "sys", "system", "support", "help",
	"helpdesk", "info", "contact", "hello", "security", "abuse", "postmaster",
	"hostmaster", "webmaster", "noreply", "no-reply", "mail", "email", "smtp",
	"www", "api", "app", "apps", "status", "billing", "payments", "pay",
	"login", "signin", "sign-in", "signup", "sign-up", "auth", "oauth", "sso",
	"account", "accounts", "dashboard", "console", "docs", "doc", "blog",
	"cdn", "static", "assets", "media", "files", "download", "downloads",
	"setup", "enterprise", "legal", "privacy", "terms", "policy", "team",
	"staff", "official", "verify", "verification", "update", "secure",
	"simplehost", "simple-host", "simplehack", "simple-hack", "test", "dev",
	"staging", "prod", "internal", "localhost",
}

// reservedNewSiteNames is the part of reservedNewNames that impersonates the
// platform or its operator. A site name only ever appears under its owner's
// address (<site>.<handle>.<SITE_DOMAIN>), so everyday names such as blog,
// docs, team or test stay free for sites.
var reservedNewSiteNames = []string{
	"admin", "administrator", "root", "sys", "system", "support", "helpdesk",
	"security", "abuse", "postmaster", "hostmaster", "webmaster", "noreply",
	"no-reply", "billing", "payments", "login", "signin", "sign-in", "signup",
	"sign-up", "auth", "oauth", "sso", "account", "accounts", "verify",
	"verification", "secure", "official", "simplehost", "simple-host",
	"simplehack", "simple-hack", "internal", "localhost",
}

func nameSet(names ...[]string) map[string]bool {
	m := map[string]bool{}
	for _, l := range names {
		for _, n := range l {
			m[n] = true
		}
	}
	return m
}

var (
	reservedNewNameSet     = nameSet(reservedNewNames)
	reservedNewSiteNameSet = nameSet(reservedNewSiteNames)
)

// labelReservedForNew reports whether a NEW handle or claimed name may not
// be label: reserved in the shared namespace, or one of reservedNewNames.
func labelReservedForNew(label string) bool {
	l := strings.ToLower(label)
	return reservedSubdomainSet[l] || reservedNewNameSet[l]
}

// platformSubdomainLabel reports whether host is exactly one DNS label under
// siteDomain (e.g. "clay" for clay.simple-host.app) and returns that label.
// Multi-label hosts (x.lab.simple-host.app), the apex and non-platform hosts
// return ok=false.
func platformSubdomainLabel(host, siteDomain string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	if siteDomain == "" || !strings.HasSuffix(host, "."+siteDomain) {
		return "", false
	}
	label := strings.TrimSuffix(host, "."+siteDomain)
	if label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

// isPlatformSubdomainHost: host is a single-label name under the platform
// domain that is not one of the platform's own hosts (content host, CNAME
// target, www).
func (h *SiteHandler) isPlatformSubdomainHost(host string) bool {
	label, ok := platformSubdomainLabel(host, h.siteDomain)
	if !ok {
		return false
	}
	host = strings.ToLower(host)
	if label == "www" || strings.EqualFold(host, h.contentHost) || strings.EqualFold(host, h.cnameTarget) {
		return false
	}
	return true
}

var errSubdomainReserved = errors.New("that name is reserved; pick another")

// claimableSubdomain validates a requested <label>.<siteDomain>: one DNS label,
// letters/digits/hyphens, no leading or trailing hyphen, not reserved, and not
// one of the platform's own hosts (content host, CNAME target).
func (h *SiteHandler) claimableSubdomain(host string) (string, error) {
	label, ok := platformSubdomainLabel(host, h.siteDomain)
	if !ok {
		return "", errors.New("a free address is one name under " + h.siteDomain + ", e.g. my-shop." + h.siteDomain)
	}
	if !labelRE.MatchString(label) || label[0] == '-' || label[len(label)-1] == '-' {
		return "", errors.New("the name may use lowercase letters, numbers and hyphens (not at the start or end), up to 63 characters")
	}
	if strings.HasPrefix(label, "xn--") {
		return "", errors.New("internationalised names are not supported")
	}
	full := label + "." + strings.ToLower(h.siteDomain)
	if labelReservedForNew(label) || strings.EqualFold(full, h.contentHost) || strings.EqualFold(full, h.cnameTarget) {
		return "", errSubdomainReserved
	}
	return full, nil
}

// bindPlatformSubdomain claims <label>.<siteDomain> for site: verified at once,
// refused if any other site holds it.
func (h *SiteHandler) bindPlatformSubdomain(w http.ResponseWriter, r *http.Request, site db.Site, requested string) {
	host, err := h.claimableSubdomain(requested)
	if err != nil {
		code := "invalid_name"
		if errors.Is(err, errSubdomainReserved) {
			code = "name_reserved"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": code})
		return
	}
	released, err := db.ClaimPlatformSubdomain(r.Context(), h.database, site.ID, host)
	if err != nil {
		if errors.Is(err, db.ErrNameIsAccountAddress) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "that name is someone's own address; pick another name", "code": "domain_taken"})
			return
		}
		if errors.Is(err, db.ErrDomainTaken) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "that address is taken by another site; pick another name", "code": "domain_taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// A site has one address of its own; the ones it replaces stop serving it
	// (a replaced claimed name redirects here: it was kept as a retired name).
	for _, d := range released {
		if err := h.disk.UnbindDomain(d); err != nil {
			log.Printf("domain: unbind previous %s for %s: %v", d, site.Name, err)
		}
	}
	if err := h.disk.BindDomain(site.UserID, site.Name, host); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", host, site.UserID, site.Name, err)
	}
	h.syncDomainRedirect(r.Context(), site.ID)
	now := time.Now()
	writeJSON(w, http.StatusOK, domainResponse{
		Domain:     host,
		Status:     "active",
		BoundAt:    &now,
		VerifiedAt: &now,
	})
}

// boundPlatformSite returns the site bound to a platform subdomain host, if
// that binding is proven (a claimed name always is).
func (h *SiteHandler) boundPlatformSite(ctx context.Context, host string) (db.SiteDomainInfo, bool) {
	if !h.isPlatformSubdomainHost(host) {
		return db.SiteDomainInfo{}, false
	}
	info, err := db.GetSiteByCustomDomain(ctx, h.database, host)
	if err != nil || !info.VerifiedAt.Valid {
		return db.SiteDomainInfo{}, false
	}
	return info, true
}

// BoundSubdomains serves claimed <name>.<siteDomain> hosts the way nginx serves
// a custom domain: /v1/ goes to the API (same-origin state, collections and
// visitor sign-in), everything else is the site's files from its live version.
// Every other request — unclaimed names included, which keep their legacy 301 —
// goes to fallback.
func (h *SiteHandler) BoundSubdomains(api, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHostName(r)
		if !h.isPlatformSubdomainHost(host) {
			fallback.ServeHTTP(w, r)
			return
		}
		info, ok := h.boundPlatformSite(r.Context(), host)
		if !ok {
			fallback.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.serveSiteFileRel(w, r, info.UserID, info.Name, r.URL.Path)
	})
}

// serveSiteFile serves one file of a site's live version, like nginx's
// `try_files $uri $uri/ =404` with `index index.html`: no directory listings,
// nothing outside the site's current directory (os.Root refuses escapes,
// symlinks included), GET and HEAD only.
//
// rel is the path inside the site ("/" is its root); a directory without its
// trailing slash redirects to the request's own path plus "/", so it works for
// a site served at a host's root and for one served under /<site>/.
func (h *SiteHandler) serveSiteFileRel(w http.ResponseWriter, r *http.Request, userID, siteName, rel string) {
	h.serveSiteFile(w, r, userID, siteName, rel, r.URL.EscapedPath())
}

// serveSiteFile is serveSiteFileRel with the (escaped, server-built) public
// path a directory redirect appends its "/" to.
func (h *SiteHandler) serveSiteFile(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A site the operator has taken down answers the take-down page on
	// every path, whichever address reached it (suspend.go).
	if h.disk.IsSuspended(userID, siteName) {
		serveTakedown(w, r)
		return
	}
	// Taken offline by its owner (offline.go): the same on every address.
	if h.disk.IsOffline(userID, siteName) {
		serveOffline(w, r)
		return
	}
	h.serveDirFile(w, r, h.disk.SiteDir(userID, siteName)+"/current", rel, publicPath)
}

// serveDirFile serves rel from dir the way serveSiteFile does (dir is a
// version tree: the live `current`, or a kept version for a preview).
func (h *SiteHandler) serveDirFile(w http.ResponseWriter, r *http.Request, dir, rel, publicPath string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()

	name := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if name == "" {
		name = "."
	}
	f, err := root.Open(name)
	if err != nil {
		h.siteNotFound(w, r, root)
		return
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		h.siteNotFound(w, r, root)
		return
	}
	if st.IsDir() {
		f.Close()
		// A directory without its trailing slash: redirect so relative links
		// resolve, as nginx does.
		if !strings.HasSuffix(r.URL.Path, "/") {
			// Only ever a path on this host: "//x" would leave it.
			target := "/" + strings.TrimLeft(publicPath, "/") + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		name = path.Join(name, "index.html")
		f, err = root.Open(name)
		if err != nil {
			h.siteNotFound(w, r, root)
			return
		}
		st, err = f.Stat()
		if err != nil || st.IsDir() {
			f.Close()
			h.siteNotFound(w, r, root)
			return
		}
	}
	defer f.Close()
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// siteNotFound answers 404 with the site's own 404.html when it has one.
func (h *SiteHandler) siteNotFound(w http.ResponseWriter, r *http.Request, root *os.Root) {
	if f, err := root.Open("404.html"); err == nil {
		defer f.Close()
		if st, err := f.Stat(); err == nil && !st.IsDir() {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, f)
			}
			return
		}
	}
	http.NotFound(w, r)
}

// siteOwnDomain returns the site's proven own domain (a verified custom domain
// or a claimed platform subdomain), or ok=false. While a newly connected
// domain is still pending, that is the site's earlier proven address, which
// keeps serving it until the new one is verified.
func (h *SiteHandler) siteOwnDomain(ctx context.Context, siteID string) (db.SiteDomainInfo, bool, error) {
	info, ok, err := db.GetSiteDomainInfo(ctx, h.database, siteID)
	if err != nil {
		return db.SiteDomainInfo{}, false, err
	}
	if !ok || info.Domain == "" {
		return db.SiteDomainInfo{}, false, nil
	}
	if info.VerifiedAt.Valid {
		return info, true, nil
	}
	if info.PreviousDomain != "" {
		return info.AsPrevious(), true, nil
	}
	return db.SiteDomainInfo{}, false, nil
}

// syncDomainRedirect points the content-host redirect marker at the site's
// proven own domain, or removes it when there is none: old
// sites.<SITE_DOMAIN> links never follow a domain that does not work yet.
func (h *SiteHandler) syncDomainRedirect(ctx context.Context, siteID string) {
	_, userID, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if err != nil {
		return
	}
	info, has, err := h.siteOwnDomain(ctx, siteID)
	if err != nil {
		return
	}
	if has {
		err = h.disk.SetDomainRedirect(userID, name, strings.ToLower(info.Domain))
	} else {
		err = h.disk.ClearDomainRedirect(userID, name)
	}
	if err != nil {
		log.Printf("domain: redirect marker for %s/%s: %v", userID, name, err)
	}
}
