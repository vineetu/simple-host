package sitesroute

import (
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

func (h *Handler) SetSitemapFilter(filter appsites.SitemapFilter) {
	h.sitemapFilter = filter
}

func (h *Handler) SetIdleCleanup(on bool) { h.IdleCleanup = on }

// SetIdleExempt sets the accounts the cleanup never touches besides the
// built-in rules: handles (IDLE_CLEANUP_EXEMPT_HANDLES) and the plugin
// reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *Handler) SetIdleExempt(handles []string, reviewerEmail string) {
	ex := sites.IdleExempt{ReviewerEmail: strings.TrimSpace(reviewerEmail), FamilySites: h.Limits.IdleExemptFamilySites}
	for _, hd := range handles {
		if hd = strings.ToLower(strings.TrimSpace(hd)); hd != "" {
			ex.Handles = append(ex.Handles, hd)
		}
	}
	h.IdleExempt = ex
}

// SetPublicBaseURL is the apex address export links are minted on
// (PUBLIC_BASE_URL, e.g. https://simple-host.app).
func (h *Handler) SetPublicBaseURL(base string) {
	h.PublicBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}

func (h *Handler) SetWorkerStatus(status func() []workers.Status) { h.WorkerStatus = status }

func (h *Handler) SetNetworkUsage(iface string) { h.Network.SetNetworkUsage(iface) }

func (h *Handler) SetTrafficLog(on bool) { h.TrafficOn = on; h.Network.SetTrafficLog(on) }

// SetAddressFamilies sets the issuer hand-off directory and starts keeping
// the list of live families (and checking them, when checks run here).
func (h *Handler) SetAddressFamilies(certDir string) {
	h.Hosts.SetFamilyCertDir(certDir)
	lim := h.Limits
	h.FamilyCheckLimiter = httpx.NewRateLimiterFor(lim.RateFamilyCheck.Burst, lim.RateFamilyCheck.Every)
	h.Caches.Rate("http.site.FamilyCheckLimiter", h.FamilyCheckLimiter, 10*time.Minute, 30*time.Minute)
	h.FamilyCheckUserLimiter = httpx.NewRateLimiterFor(lim.RateFamilyCheckUser.Burst, lim.RateFamilyCheckUser.Every)
	h.Caches.Rate("http.site.FamilyCheckUserLimiter", h.FamilyCheckUserLimiter, 10*time.Minute, 30*time.Minute)
}

// SetTeamSiteFlatHosts sets TEAM_SITE_FLAT_HOSTS, including the served
// auth.js patch (basetext.go) that teaches it to take the part of a flat
// team-site host's one label before "--" as the site name.
func (h *Handler) SetTeamSiteFlatHosts(on bool) {
	h.Hosts.SetTeamSiteFlatHosts(on)
	web.SetTeamSiteFlatHostsText(on && h.Ed.Routes().HostedEvents)
}
