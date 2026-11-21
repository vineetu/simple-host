package hackhttp

import (
	"context"
	"log"
	"net/url"
	"strings"

	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// syncEventMarkers puts the event's team sites down or back with the event;
// false (after logging) when a marker could not be written.
func (h *HackHandler) syncEventMarkers(ctx context.Context, ev hackmodel.Event) bool {
	if h.links.hooks == nil {
		return true
	}
	if err := h.links.hooks.SyncAccountMarkers(ctx, ev.AccountID); err != nil {
		log.Printf("hack: take-down markers of %s: %v", ev.Slug, err)
		return false
	}
	return true
}

func (h *siteLinks) removeEventFiles(ev hackmodel.Event) {
	if h.hooks == nil {
		return
	}
	if err := h.hooks.RemoveAccountFiles(ev.AccountID, ev.Slug); err != nil {
		log.Printf("hack: remove files of deleted event %s: %v", ev.Slug, err)
	}
}

// siteLinks are the instance's addresses and what Hack asks of the team-site
// side (hooks: the SiteHandler; nil in tests that do not need it, and team
// addresses then count as not ready).
type siteLinks struct {
	hooks         hackSiteHooks
	teams         *teams.Service
	publicBaseURL string
	siteDomain    string
}

// teamSitesReady: the event's team addresses have their certificate.
func (h *siteLinks) teamSitesReady(eventSlug string) bool {
	return h.hooks != nil && h.hooks.TeamSitesReady(eventSlug)
}

// teamSiteFlatHostsOn: TEAM_SITE_FLAT_HOSTS is on (h.links.hooks, the SiteHandler,
// owns the config; false with no SiteHandler wired).
func (h *siteLinks) teamSiteFlatHostsOn() bool {
	return h.hooks != nil && h.hooks.TeamSiteFlatHostsOn()
}

// teamSiteURL is https://<team>.<event>.<SITE_DOMAIN>/, or the flat
// <team>--<event>.<SITE_DOMAIN> form under TEAM_SITE_FLAT_HOSTS — h.links.hooks
// (the SiteHandler) owns that config, through the one function
// (appsites.TeamSiteHost) both packages call, so the format never scatters.
func (h *siteLinks) teamSiteURL(eventSlug, teamSlug string) string {
	if h.hooks != nil {
		return h.hooks.TeamSiteURL(eventSlug, teamSlug)
	}
	// No SiteHandler wired (incomplete test/deployment wiring): there is no
	// TEAM_SITE_FLAT_HOSTS config to read here, so this always falls back to
	// the two-level form.
	return "https://" + appsites.TeamSiteHost(eventSlug, teamSlug, h.siteDomain, false) + "/"
}

func (h *siteLinks) EventURL(slug string) string {
	return "https://" + slug + "." + h.siteDomain + "/"
}

func (h *siteLinks) joinURL(code string) string {
	return h.publicBaseURL + "/join/" + code
}

func (h *siteLinks) judgeURL(code string) string {
	return h.publicBaseURL + "/judge/" + code
}

// teamLinkURL is https://<hack host>/team/<code>: instant join, never event
// admission (checked at GET /v1/hack/team-links/{code} and at join).
func (h *siteLinks) teamLinkURL(code string) string {
	return h.publicBaseURL + "/team/" + code
}

func (h *HackHandler) manageURL(slug string) string {
	return h.links.publicBaseURL + "/e/" + slug + "/manage"
}

// teamCodeFromInput accepts either a bare team code or a pasted team link
// (https://<hack host>/team/<code>): when s parses as a URL on this host, or
// simply contains "/team/" (a bare path, or a link previewed/pasted before TLS
// terms are known), the last path segment is taken as the code.
func (h *siteLinks) teamCodeFromInput(s string) string {
	s = strings.TrimSpace(s)
	useLastSegment := strings.Contains(s, "/team/")
	if !useLastSegment {
		if u, err := url.Parse(s); err == nil && u.Host != "" && strings.EqualFold(u.Host, h.teamLinkHost()) {
			useLastSegment = true
		}
	}
	if useLastSegment {
		// Strip the query/fragment first, then trim trailing slashes: removing "?source=chat" from ".../abcd1234/?source=chat" leaves a trailing slash, which would make the last segment empty if not trimmed too.
		trimmed := s
		if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
			trimmed = trimmed[:i]
		}
		trimmed = strings.TrimRight(trimmed, "/")
		if i := strings.LastIndex(trimmed, "/"); i >= 0 {
			s = trimmed[i+1:]
		} else {
			s = trimmed
		}
	}
	return events.NormalizeCode(s)
}

func (h *siteLinks) teamLinkHost() string {
	if u, err := url.Parse(h.publicBaseURL); err == nil {
		return u.Host
	}
	return ""
}
