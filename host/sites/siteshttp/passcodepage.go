package siteshttp

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// passcodePageHandler answers /internal/passcode/{rest...}, where nginx and
// Caddy send a request for a site whose folder carries the passcode marker.
// On a custom domain, a claimed name or a family address the host names the
// site: the gate, or the file once unlocked (a family address of a site
// that lives on a domain of its own redirects there first). On the content host the path does
// (/<handle>/<site>/...): that site's own address. Anything else — a
// hand-made vhost the app does not know — gets the protected page with no
// form: closed, never the file.
func (h *SiteHandler) passcodePageHandler(w http.ResponseWriter, r *http.Request) {
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	host := httpx.HostName(r)
	if h.Hosts.IsSharedHost(host) {
		handle, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		name, after, _ := strings.Cut(tail, "/")
		if handle == "" || name == "" {
			h.NotFound(w, r)
			return
		}
		r.SetPathValue("handle", handle)
		r.SetPathValue("sitename", name)
		r.SetPathValue("rest", after)
		h.contentHostRedirect(w, r)
		return
	}
	// From here the host names the site: everything below (the gate's
	// next, /robots.txt) sees the path the visitor asked for, not the
	// rewrite.
	r = web.WithVisitorPath(r)
	// A binding still waiting for its proof is served too: nginx serves the
	// same domain straight from disk when the site has no marker, and the
	// proof itself fetches this page.
	info, err := h.siteFacts().GetSiteByCustomDomain(r.Context(), host)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("passcode: domain %s: %v", host, err)
		}
		// A family address (familyhost.go): the host names the site too.
		if m, ok, ferr := h.Hosts.FamilySiteForHost(r.Context(), host); ferr == nil && ok {
			h.serveFamilyFile(w, r, m, rest, (&url.URL{Path: rest}).EscapedPath())
			return
		} else if ferr != nil {
			log.Printf("passcode: family host %s: %v", host, ferr)
			web.RenderServiceErrorPage(w, r, h.Pipeline())
			return
		}
		h.Gate.ServeHTTP(w, r) // no site: the closed page
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFile(w, r, info.UserID, info.Name, rest, r.URL.EscapedPath())
}
