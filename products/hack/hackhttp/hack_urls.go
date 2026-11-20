package hackhttp

import (
	"context"
	"log"
	"net/url"
	"strings"

	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// syncEventMarkers puts the event's team sites down or back with the event;
// false (after logging) when a marker could not be written.
func (h *HackHandler) syncEventMarkers(ctx context.Context, ev hackmodel.Event) bool {
	if h.sites == nil {
		return true
	}
	if err := h.sites.SyncAccountMarkers(ctx, ev.AccountID); err != nil {
		log.Printf("hack: take-down markers of %s: %v", ev.Slug, err)
		return false
	}
	return true
}

// removeEventFiles deletes a deleted event's team site files.
func (h *HackHandler) removeEventFiles(ev hackmodel.Event) {
	if h.sites == nil {
		return
	}
	if err := h.sites.RemoveAccountFiles(ev.AccountID, ev.Slug); err != nil {
		log.Printf("hack: remove files of deleted event %s: %v", ev.Slug, err)
	}
}

// teamSitesReady: the event's team addresses have their certificate.
func (h *hackBase) teamSitesReady(eventSlug string) bool {
	return h.sites != nil && h.sites.TeamSitesReady(eventSlug)
}

// teamSiteFlatHostsOn: TEAM_SITE_FLAT_HOSTS is on (h.sites, the SiteHandler,
// owns the config; false with no SiteHandler wired).
func (h *hackBase) teamSiteFlatHostsOn() bool {
	return h.sites != nil && h.sites.TeamSiteFlatHostsOn()
}

// teamSiteURL is https://<team>.<event>.<SITE_DOMAIN>/, or the flat
// <team>--<event>.<SITE_DOMAIN> form under TEAM_SITE_FLAT_HOSTS — h.sites
// (the SiteHandler) owns that config, through the one function
// (appsites.TeamSiteHost) both packages call, so the format never scatters.
func (h *hackBase) teamSiteURL(eventSlug, teamSlug string) string {
	if h.sites != nil {
		return h.sites.TeamSiteURL(eventSlug, teamSlug)
	}
	// No SiteHandler wired (incomplete test/deployment wiring): there is no
	// TEAM_SITE_FLAT_HOSTS config to read here, so this always falls back to
	// the two-level form.
	return "https://" + appsites.TeamSiteHost(eventSlug, teamSlug, h.siteDomain, false) + "/"
}

// EventURL is https://<slug>.<siteDomain>/
func (h *hackBase) EventURL(slug string) string {
	return "https://" + slug + "." + h.siteDomain + "/"
}

func (h *hackBase) joinURL(code string) string {
	return h.publicBaseURL + "/join/" + code
}

func (h *hackBase) judgeURL(code string) string {
	return h.publicBaseURL + "/judge/" + code
}

// teamLinkURL is https://<hack host>/team/<code>: instant join, never event
// admission (checked at GET /v1/hack/team-links/{code} and at join).
func (h *hackBase) teamLinkURL(code string) string {
	return h.publicBaseURL + "/team/" + code
}

func (h *HackHandler) manageURL(slug string) string {
	return h.publicBaseURL + "/e/" + slug + "/manage"
}

// teamCodeFromInput accepts either a bare team code or a pasted team link
// (https://<hack host>/team/<code>): when s parses as a URL on this host,
// or simply contains "/team/" (a bare path, or a link previewed/pasted
// before TLS terms are known), the last path segment is taken as the code.
// Either way the result is normalised like any other code, so a link and
// its code land on the same team.
func (h *hackBase) teamCodeFromInput(s string) string {
	s = strings.TrimSpace(s)
	useLastSegment := strings.Contains(s, "/team/")
	if !useLastSegment {
		if u, err := url.Parse(s); err == nil && u.Host != "" && strings.EqualFold(u.Host, h.teamLinkHost()) {
			useLastSegment = true
		}
	}
	if useLastSegment {
		// Strip the query/fragment before trimming trailing slashes, not
		// after: ".../abcd1234/?source=chat" has no trailing slash to trim
		// yet, but removing "?source=chat" first leaves one behind
		// (".../abcd1234/"), which then makes the last segment empty
		// unless it is trimmed too (Codex review, 2026-11-01).
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

// teamLinkHost is the host team links are served from.
func (h *hackBase) teamLinkHost() string {
	if u, err := url.Parse(h.publicBaseURL); err == nil {
		return u.Host
	}
	return ""
}
