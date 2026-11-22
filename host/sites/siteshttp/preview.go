package siteshttp

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

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
			w.Header().Add("Content-Security-Policy", "frame-ancestors 'self' "+h.MainSiteURL())
		}
		h.serveDirFile(w, r, h.Disk.VersionDir(site.UserID, site.Name, n), "/"+rest, r.URL.EscapedPath())
	})
	h.Gate.ServeHTTP(w, appsites.WithServing(r, appsites.Serving{UserID: site.UserID, Name: site.Name, Rel: "/" + rest, FrameOnly: true, Then: then}))
}

func (h *SiteHandler) renderPreviewExpired(w http.ResponseWriter, r *http.Request) {
	h.renderNotFoundPage(w, r, "This preview link has expired",
		"Preview links work for "+web.LimitSpan(h.Limits.PreviewLinkTTL)+". Make a new one from Versions on your Simple Host page, or ask your AI app.",
		h.MainSiteURL(), "Go to "+h.SiteDomain)
}
