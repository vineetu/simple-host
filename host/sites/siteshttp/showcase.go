package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	xhtml "golang.org/x/net/html"
)

// publicSitesBase reconstructs the scheme://host the browser reached the content
// host on (e.g. https://sites.simple-host.app), used to build site + showcase
// links. Behind nginx, Host is preserved and X-Forwarded-Proto carries scheme.
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

func (h *SiteHandler) mainSiteURL() string {
	d := h.SiteDomain
	if d == "" {
		d = "simple-host.app"
	}
	return "https://" + d
}

// showcase renders a user's public profile at sites.<domain>/<handle>. nginx
// proxies the single-segment path here as GET /internal/showcase/{handle}. The
// server always renders the PUBLIC view (safe default); the page hydrates into
// the owner view client-side when a matching same-origin API key is present.
func (h *SiteHandler) Showcase(w http.ResponseWriter, r *http.Request) {
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if h.redirectHandleAlias(w, r, handle) {
		return
	}
	if !h.Ed.Routes().HostedEvents && !h.Hosts.Addressing().PersonHostsOn() && sites.ShowcaseHandle(handle) {
		user, err := h.storeAccounts().GetUserByHandle(r.Context(), handle)
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

// redirectHandleAlias: handle is an old handle kept as an alias (the account
// changed its address), so /<old> 302s to /<current> on the same host — the
// owner app on the base origin, the showcase on the content host.
func (h *SiteHandler) redirectHandleAlias(w http.ResponseWriter, r *http.Request, handle string) bool {
	if !sites.ShowcaseHandle(handle) {
		return false
	}
	if _, err := h.storeAccounts().GetUserByHandle(r.Context(), handle); err == nil {
		return false
	}
	current, err := h.storeAccounts().ResolveHandleAlias(r.Context(), handle)
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

// OwnerAppOrStatic serves the per-user OWNER APP on the base origin
// (<domain>/<handle>) when the single path segment resolves to a real handle,
// otherwise falls through to the static file server (landing page, docs, install
// page, assets, SPA). The page it renders hydrates into the full owner dashboard
// client-side, because on the base origin the API key is already in localStorage
// from login — no paste, no redirect. The cheap dot/charset filter means the DB
// lookup only fires for handle-shaped paths, never for a static asset request.
func (h *SiteHandler) OwnerAppOrStatic(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg := strings.Trim(r.URL.Path, "/")
		// The hackathon platform has no person pages (hack_mode.go).
		if seg != "" && !h.Ed.Routes().HostedEvents && !strings.ContainsAny(seg, "/.") &&
			sites.ShowcaseHandle(strings.ToLower(seg)) {
			if _, err := h.storeAccounts().GetUserByHandle(r.Context(), strings.ToLower(seg)); err == nil {
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

// contentBaseURL is the absolute base for user site + showcase links — always the
// content host (sites.<domain>), regardless of which origin served the page, so
// the rendered links are correct whether it came from the base app or the
// content host.
func (h *SiteHandler) contentBaseURL() string {
	host := h.ContentHost
	if host == "" {
		host = "sites." + h.SiteDomain
	}
	return "https://" + host
}

// renderShowcase renders the per-user showcase for `handle`. Same output on both
// origins (all links absolute): served on the content host it's the public,
// read-only view; served on the base app it hydrates into the owner dashboard.
func (h *SiteHandler) renderShowcase(w http.ResponseWriter, r *http.Request, handle string) {
	if handle == "" || !sites.ShowcaseHandle(handle) {
		h.RenderNotFound(w, r, "/"+handle)
		return
	}

	user, err := h.storeAccounts().GetUserByHandle(r.Context(), handle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.RenderNotFound(w, r, "/"+handle)
			return
		}
		h.renderServiceError(w, r)
		return
	}

	data, err := h.publicShowcaseData(r.Context(), user)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}

	page, err := h.Pipeline().ShowcasePage(h.Pipeline().ChromeDataFor(r, h.chromeBase(r)), data)
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

// publicShowcaseData is the single public projection used by HTML and the JSON feed.
func (h *SiteHandler) publicShowcaseData(ctx context.Context, user model.User) (web.ShowcaseData, error) {
	sites, err := h.SitesSvc().ListSitesByUser(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	bio, err := h.storeAccounts().GetShowcaseBio(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	prefs, err := h.SitesSvc().ShowcasePreferences(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	handle := user.Handle.String
	data := web.ShowcaseData{Bio: bio, Handle: handle, SitesBaseURL: h.contentBaseURL(), PublicShowcaseURL: h.PersonPageURL(handle), OwnerAppURL: h.mainSiteURL() + "/" + handle, MainURL: h.mainSiteURL(), Sites: []web.ShowcaseSite{}}
	for _, s := range sites {
		if s.Visibility != "public" || !s.OpenToAnyone() {
			continue
		}
		title, desc := h.showcaseMetadata(s)
		data.Sites = append(data.Sites, web.ShowcaseSite{Name: s.Name, URL: h.SiteURL(handle, s.Name), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Visibility: "public", Title: title, Description: desc, Pinned: prefs[s.ID].Pinned, Order: prefs[s.ID].Order})
	}
	sort.SliceStable(data.Sites, func(i, j int) bool {
		if data.Sites[i].Pinned != data.Sites[j].Pinned {
			return data.Sites[i].Pinned
		}
		return data.Sites[i].Order < data.Sites[j].Order
	})
	return data, nil
}

// Metadata comes only from a visible site's deployed index, never a remote fetch.
func (h *SiteHandler) showcaseMetadata(s model.Site) (title, description string) {
	f, _, err := h.Disk.OpenFile(context.Background(), sites.FileVersion{UserID: s.UserID, SiteName: s.Name}, "index.html")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	z := xhtml.NewTokenizer(io.LimitReader(f, 256<<10))
	inTitle := false
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.TrimSpace(title), strings.TrimSpace(description)
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "title" {
				inTitle = true
			}
			if t.Data == "meta" {
				var name, content string
				for _, a := range t.Attr {
					if a.Key == "name" {
						name = strings.ToLower(a.Val)
					}
					if a.Key == "content" {
						content = a.Val
					}
				}
				if name == "description" && description == "" {
					description = content
				}
			}
		case xhtml.EndTagToken:
			if z.Token().Data == "title" {
				inTitle = false
			}
		case xhtml.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		}
	}
}

func (h *SiteHandler) showcaseFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
	w.Header().Set("Cache-Control", "no-store")
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if h.Ed.Routes().HostedEvents || !sites.ShowcaseHandle(handle) {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "not found", Code: "not_found"})
		return
	}
	user, err := h.storeAccounts().GetUserByHandle(r.Context(), handle)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Every spelling/host uses the user ID.
	if h.ShowcaseLimiter != nil && !h.ShowcaseLimiter.Allow("showcase:"+user.ID+":"+httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return
	}
	data, err := h.publicShowcaseData(r.Context(), user)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteJSON(w, 200, map[string]any{"handle": data.Handle, "bio": data.Bio, "sites": data.Sites})
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

// renamedContentPath: uri (a content-host request URI, escaped, with any
// query) is /<handle>/<name>/... where name is an old name of one of that
// person's renamed sites and no site has it now. Returns the site's current
// address with the rest of the path and the query.
func (h *SiteHandler) renamedContentPath(ctx context.Context, uri string) (string, bool) {
	p, query, hasQuery := strings.Cut(uri, "?")
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	handle, name := strings.ToLower(parts[0]), parts[1]
	user, err := h.siteFacts().ContentPathOwner(ctx, handle, name)
	if err != nil {
		return "", false
	}
	if _, err := h.siteFacts().AddressedSite(ctx, user.ID, name); !errors.Is(err, sql.ErrNoRows) {
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

// RenderNotFound serves the branded 404. If the first path segment is a real
// handle, the back-link points at that user's showcase (owner's stated
// priority); otherwise it points at the main page. Only regex-validated handles
// that resolve to a real user are ever echoed back into the page.
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
	backURL := h.mainSiteURL()
	// Baseline, unconditional literal on the hosted service, true for
	// simple-hack.app too (same team, deliberate cross-brand reference) --
	// must stay byte-identical there. A self-hosted install has no business
	// sending anyone to simple-host.app, so it names itself instead.
	backLabel := "Go to simple-host.app"
	if h.Pipeline().HostedOwner() == "" {
		backLabel = "Go to " + h.SiteDomain
	}

	if len(segs) >= 1 {
		handle := strings.ToLower(segs[0])
		if sites.ShowcaseHandle(handle) {
			if _, err := h.siteFacts().GetUserByHandle(r.Context(), handle); err == nil {
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

// renderNotFoundPage writes the branded 404 with the given words and way back.
func (h *SiteHandler) renderNotFoundPage(w http.ResponseWriter, r *http.Request, message, subtext, backURL, backLabel string) {
	w.Header().Add("Vary", "Accept")
	if httpx.AcceptsMarkdown(r) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("# 404: Page not found\n\nThe page you requested does not exist.\n"))
		return
	}
	h.renderMessagePage(w, r, http.StatusNotFound, message, subtext, backURL, backLabel)
}

// renderMessagePage is the branded one-message page (the not-found page's
// layout) with any status. message and subtext are inserted as given: escape
// anything that did not come from this code.
func (h *SiteHandler) renderMessagePage(w http.ResponseWriter, r *http.Request, status int, message, subtext, backURL, backLabel string) {
	h.Pipeline().WriteMessagePage(w, r, h.chromeBase(r), status, message, subtext, backURL, backLabel, "")
}

// RenderServiceErrorForTest is renderServiceError, exported as a test hook
// (cleanup 3.2 slice 3): the moved hack_ink_browser_test.go (an opt-in
// Playwright rehearsal fixture) mounts it at a debug path.
func (h *SiteHandler) RenderServiceErrorForTest(w http.ResponseWriter, r *http.Request) {
	h.renderServiceError(w, r)
}

func (h *SiteHandler) renderServiceError(w http.ResponseWriter, r *http.Request) {
	web.RenderServiceErrorPage(w, r, h.Pipeline())
}
