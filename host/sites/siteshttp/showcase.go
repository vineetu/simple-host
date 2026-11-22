package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// publicSitesBase reconstructs the scheme://host the browser reached the content host on. Behind nginx,
// Host is preserved and X-Forwarded-Proto carries the scheme.
func (h *SiteHandler) publicSitesBase(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = h.ContentHost
	}
	return scheme + "://" + host
}

// showcase renders a user's public profile at sites.<domain>/<handle> (nginx proxies it here as
// GET /internal/showcase/{handle}). Always the PUBLIC view; the page hydrates into the owner view
// client-side when a matching same-origin API key is present.
func (h *SiteHandler) Showcase(w http.ResponseWriter, r *http.Request) {
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if h.redirectHandleAlias(w, r, handle) {
		return
	}
	if !h.Ed.Routes().HostedEvents && !h.Hosts.Addressing().PersonHostsOn() && sites.ShowcaseHandle(handle) {
		user, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle)
		if err == nil {
			s, has, err := h.SitesSvc().HomeSite(r.Context(), user.ID)
			if err != nil {
				h.renderServiceError(w, r)
				return
			}
			if has && s.Live() {
				w.Header().Set("Cache-Control", "no-store")
				target := h.SiteURL(handle, s.Name)
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
		}
	}
	h.renderShowcase(w, r, handle)
}

// redirectHandleAlias: handle is an old handle kept as an alias, so /<old> 302s to /<current> on the same host.
func (h *SiteHandler) redirectHandleAlias(w http.ResponseWriter, r *http.Request, handle string) bool {
	if !sites.ShowcaseHandle(handle) {
		return false
	}
	if _, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle); err == nil {
		return false
	}
	current, err := h.StoreAccounts().ResolveHandleAlias(r.Context(), handle)
	if err != nil || current == "" || current == handle {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	target := "/" + current
	if r.URL.RawQuery != "" {
		// Keeps deep links such as ?site=<site>&open=passcode from an old chat working.
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
	return true
}

// OwnerAppOrStatic serves the per-user OWNER APP on the base origin (<domain>/<handle>) when the single path
// segment resolves to a real handle, otherwise falls through to the static file server. The cheap dot/charset
// filter keeps the DB lookup off static-asset requests.
func (h *SiteHandler) OwnerAppOrStatic(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg := strings.Trim(r.URL.Path, "/")
		if seg != "" && !h.Ed.Routes().HostedEvents && !strings.ContainsAny(seg, "/.") &&
			sites.ShowcaseHandle(strings.ToLower(seg)) {
			if _, err := h.StoreAccounts().GetUserByHandle(r.Context(), strings.ToLower(seg)); err == nil {
				h.renderShowcase(w, r, strings.ToLower(seg))
				return
			}
			if h.redirectHandleAlias(w, r, strings.ToLower(seg)) {
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// renderShowcase renders the per-user showcase for handle: public read-only view on the content host,
// hydrates into the owner dashboard on the base app.
func (h *SiteHandler) renderShowcase(w http.ResponseWriter, r *http.Request, handle string) {
	if handle == "" || !sites.ShowcaseHandle(handle) {
		h.RenderNotFound(w, r, "/"+handle)
		return
	}

	user, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.RenderNotFound(w, r, "/"+handle)
			return
		}
		h.renderServiceError(w, r)
		return
	}

	data, err := h.PublicShowcaseData(r.Context(), user)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}

	page, err := h.Pages().ShowcasePage(h.Pages().ChromeDataFor(r, h.chromeBase(r)), data)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "index")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(httpx.StampNonce(r, page))
}

// notFound is the nginx error_page fallback. nginx sends file misses here and
// forwards the original path as X-Original-URI so we can tailor the back-link.
func (h *SiteHandler) NotFound(w http.ResponseWriter, r *http.Request) {
	orig := r.Header.Get("X-Original-URI")
	if orig == "" {
		orig = r.URL.Path
	}
	// sites.<domain>/<handle>/<old-name>/...: nginx finds no folder for a
	// renamed site's old name; send the link to the site's current address.
	if target, ok := h.renamedContentPath(r.Context(), orig); ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	h.RenderNotFound(w, r, orig)
}

// renamedContentPath: uri is /<handle>/<name>/... where name is an old name of one of that person's
// renamed sites and no site has it now. Returns the site's current address with the rest of the path and query.
func (h *SiteHandler) renamedContentPath(ctx context.Context, uri string) (string, bool) {
	p, query, hasQuery := strings.Cut(uri, "?")
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	handle, name := strings.ToLower(parts[0]), parts[1]
	user, err := h.SitesSvc().ContentPathOwner(ctx, handle, name)
	if err != nil {
		return "", false
	}
	if _, err := h.SitesSvc().AddressedSite(ctx, user.ID, name); !errors.Is(err, sql.ErrNoRows) {
		return "", false // a real site (a missing file), or a lookup error
	}
	rest := ""
	if len(parts) == 3 {
		rest = parts[2]
	}
	target, ok := h.renamedSiteAddress(ctx, user.ID, name, "/"+rest)
	if !ok {
		return "", false
	}
	if hasQuery {
		target += "?" + query
	}
	return target, true
}

// RenderNotFound serves the branded 404. If the first path segment is a real handle the back-link points at
// that user's showcase, else the main page. Only regex-validated handles that resolve to a real user are echoed.
func (h *SiteHandler) RenderNotFound(w http.ResponseWriter, r *http.Request, origPath string) {
	if i := strings.IndexByte(origPath, '?'); i >= 0 {
		origPath = origPath[:i]
	}
	var segs []string
	for _, s := range strings.Split(strings.Trim(origPath, "/"), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}

	base := h.publicSitesBase(r)
	message := "Page not found"
	subtext := "The page you’re looking for doesn’t exist."
	backURL := h.MainSiteURL()
	// Baseline, unconditional literal on the hosted service, true for
	// simple-hack.app too (same team, deliberate cross-brand reference) --
	// must stay byte-identical there. A self-hosted install has no business
	// sending anyone to simple-host.app, so it names itself instead.
	backLabel := "Go to simple-host.app"
	if h.Pages().HostedOwner() == "" {
		backLabel = "Go to " + h.SiteDomain
	}

	if len(segs) >= 1 {
		handle := strings.ToLower(segs[0])
		if sites.ShowcaseHandle(handle) {
			if _, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle); err == nil {
				message = "That page isn’t here"
				subtext = "This site or page doesn’t exist under @" + handle + "."
				backURL = h.PersonPageURL(handle)
				if backURL == "" {
					backURL = base + "/" + handle
				}
				backLabel = "Back to @" + handle + "’s sites"
			}
		}
	}

	h.renderNotFoundPage(w, r, message, subtext, backURL, backLabel)
}

func (h *SiteHandler) renderNotFoundPage(w http.ResponseWriter, r *http.Request, message, subtext, backURL, backLabel string) {
	w.Header().Add("Vary", "Accept")
	if httpx.AcceptsMarkdown(r) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("# 404: Page not found\n\nThe page you requested does not exist.\n"))
		return
	}
	// message and subtext are inserted as given: escape anything that did not
	// come from this code.
	h.Pages().WriteMessagePage(w, r, h.chromeBase(r), http.StatusNotFound, message, subtext, backURL, backLabel, "")
}

// RenderServiceErrorForTest is renderServiceError, exported for a
// cross-package browser fixture.
func (h *SiteHandler) RenderServiceErrorForTest(w http.ResponseWriter, r *http.Request) {
	h.renderServiceError(w, r)
}

func (h *SiteHandler) renderServiceError(w http.ResponseWriter, r *http.Request) {
	web.RenderServiceErrorPage(w, r, h.Pages())
}
