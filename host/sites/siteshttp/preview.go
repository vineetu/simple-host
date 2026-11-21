package siteshttp

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// SignPreviewToken binds a token to one site, one version and an expiry.
func (h *SiteHandler) SignPreviewToken(siteID string, version int, expires time.Time) string {
	payload := siteID + "." + strconv.Itoa(version) + "." + strconv.FormatInt(expires.Unix(), 10)
	return httpx.SealLink(h.ExportKey, web.PreviewLinkDomain, []byte(payload))
}

// CheckPreviewToken returns the site and version a token was minted for, if
// its authentication holds and it has not expired.
func (h *SiteHandler) CheckPreviewToken(token string, now time.Time) (siteID string, version int, err error) {
	return web.OpenPreviewToken(h.ExportKey, h.Limits.PreviewLinkTTL, token, now)
}

// previewBase is where a site's previews are served: "https://<host>" plus
// the path prefix up to and including "/__preview/". ok=false when the site
// has no address of its own to preview on (site and person hosts off, or a
// site pinned to the content host).
func (h *SiteHandler) previewBase(site model.Site) (string, bool) {
	handle := site.OwnerHandle
	switch {
	case handle == "":
		return "", false
	case h.Hosts.Addressing().SiteHostLiveOn(handle, site.Name, h.Hosts.Addressing().HandoutBase()):
		return "https://" + h.Hosts.Addressing().SiteHostOn(handle, site.Name, h.Hosts.Addressing().HandoutBase()) + "/" + web.PreviewPathSegment + "/", true
	case h.Ed.Storage().EventSites:
		// Person-path addresses are never served on the hackathon platform:
		// a team's work lives on its own origin only.
		return "", false
	case h.Hosts.Addressing().PersonAddressFor(handle, site.Name):
		return "https://" + h.Hosts.Addressing().PersonHostOn(handle, h.Hosts.Addressing().HandoutBase()) + "/" + site.Name + "/" + web.PreviewPathSegment + "/", true
	}
	return "", false
}

// SharedOrigin reports whether every site is served on the content host's
// one origin (no per-site or per-person addresses: a small box).
func (h *SiteHandler) SharedOrigin() bool { return h.Hosts.Addressing().SharedOrigin() }

// noPreviewWhy says why a site has no preview link: previews are served on the site's own address, which a
// server that gives sites none (SITE_HOSTS and PERSON_HOSTS off, a small box) does not have.
func (h *SiteHandler) noPreviewWhy() string {
	if h.SharedOrigin() {
		return "preview links need a per-site address, and this server serves every site on one shared address, so it makes none. " +
			"To see a stored version, make it live (PUT /v1/sites/<site>/active-version) and switch back if it is not right"
	}
	return "this site has no address of its own yet to preview on (its address is still being set up); try again later"
}

func (h *SiteHandler) previewLink(site model.Site, n int) (string, time.Time, bool) {
	base, ok := h.previewBase(site)
	if !ok {
		return "", time.Time{}, false
	}
	expires := time.Now().Add(h.Limits.PreviewLinkTTL).Truncate(time.Second)
	return base + strconv.Itoa(n) + "/" + h.SignPreviewToken(site.ID, n, expires) + "/", expires, true
}

// createPreviewLink is POST /v1/sites/{sitename}/versions/{version}/preview-link: an hour-long owner preview address.
func (h *SiteHandler) createPreviewLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	site, err := h.SiteForCaller(r, user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, name)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	kept, err := h.SitesSvc().VersionKept(r.Context(), site.ID, n)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !kept {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	link, expires, ok := h.previewLink(site, n)
	if !ok {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: h.noPreviewWhy(), Code: "preview_unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":       site.Name,
		"version":    n,
		"live":       n == site.ActiveVersion,
		"url":        link,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"expires_in": int(h.Limits.PreviewLinkTTL.Seconds()),
	})
}

// servePreview answers <prefix><n>/<token>/<rest>; tail is the decoded path after "__preview/".
func (h *SiteHandler) servePreview(w http.ResponseWriter, r *http.Request, site model.Site, tail, prefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	// Same-origin requests from a preview name it in Referer, which is how
	// they are told apart (PreviewReferer).
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	nStr, after, _ := strings.Cut(tail, "/")
	token, rest, hasSlash := strings.Cut(after, "/")
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 || token == "" {
		h.renderPreviewExpired(w, r)
		return
	}
	siteID, version, err := h.CheckPreviewToken(token, time.Now())
	if err != nil || siteID != site.ID || version != n {
		h.renderPreviewExpired(w, r)
		return
	}
	if !hasSlash {
		target := prefix + nStr + "/" + token + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if h.Disk.IsSuspended(site.UserID, site.Name) {
		web.ServeTakedownPage(w, r, h.Pages())
		return
	}
	kept, err := h.SitesSvc().VersionKept(r.Context(), site.ID, n)
	if err != nil {
		log.Printf("preview %s v%d: %v", site.ID, n, err)
		h.renderServiceError(w, r)
		return
	}
	if !kept {
		h.renderPreviewExpired(w, r)
		return
	}
	// The link is the admission: the gate adds only the owner's frame guard.
	then := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Ed.Storage().JudgePreviewFrames && !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors") {
			// The judge's scoring screen on the app origin frames the submitted version of a team site
			// (hackAppCSP, entryListItem's frameable). Only that origin: a CSP frame-ancestors also overrides the
			// proxy's X-Frame-Options SAMEORIGIN, which would otherwise refuse the app. A signed-in member's own
			// pages keep ownerFrameGuard's 'self' (checked above).
			w.Header().Add("Content-Security-Policy", "frame-ancestors 'self' "+h.mainSiteURL())
		}
		h.serveDirFile(w, r, h.Disk.VersionDir(site.UserID, site.Name, n), "/"+rest, r.URL.EscapedPath())
	})
	h.Gate.ServeHTTP(w, appsites.WithServing(r, appsites.Serving{UserID: site.UserID, Name: site.Name, Rel: "/" + rest, FrameOnly: true, Then: then}))
}

func (h *SiteHandler) renderPreviewExpired(w http.ResponseWriter, r *http.Request) {
	h.renderNotFoundPage(w, r, "This preview link has expired",
		"Preview links work for "+web.LimitSpan(h.Limits.PreviewLinkTTL)+". Make a new one from Versions on your Simple Host page, or ask your AI app.",
		h.mainSiteURL(), "Go to "+h.SiteDomain)
}
