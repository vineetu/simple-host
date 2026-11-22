package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/export"
)

// Team sites on the hackathon platform (EVENTS=hosted; docs/designs/simple-hack-platform.md).
//
// A team's site is the site named after the team of the event's holding
// account, served at <team>.<event>.<SITE_DOMAIN> once *.<event>.<SITE_DOMAIN>
// has its certificate. Its editors (the team's members, with their own
// sign-in and owner=<event>) act on it, and only on that one site
// (auth.ScopeGate holds the routes; the product's write gate the rest).
// Every deploy, upload and rollback checks the team's deadline inside its
// transaction, holding the team row FOR SHARE, so the deadline pin
// (db.PinDueTeams) sees exactly what was let in before the deadline.

// TeamSitesReady: *.<event>.<SITE_DOMAIN> is served with its certificate. A
// pure check: certificates are asked for only for events that are open,
// building or closed (HackHandler.requestEventCert, the sweep, a deploy),
// never from a page view, so drafts and deleted events spend no budget. Team
// sites are offered and deployed only then, so a
// team's work never lands anywhere but its own origin.
//
// siteCertReady (sitebase.go's siteCertReadyOn) is itself flat-aware: under
// TEAM_SITE_FLAT_HOSTS it never waits on a per-event marker, since team
// sites live under the instance's own *.<base> wildcard instead.
func (h *SiteHandler) TeamSitesReady(eventSlug string) bool {
	return eventSlug != "" && h.Hosts.Addressing().SiteHostsOn() && h.Hosts.Addressing().SiteCertReadyOn(eventSlug, h.Hosts.Addressing().HandoutBase())
}

func (h *SiteHandler) TeamSiteFlatHostsOn() bool { return h.Hosts.Addressing().TeamSiteFlatHostsOn() }

// TeamSiteURL is the address of a team's site: siteHostFor (sitehost.go) is
// itself flat-aware (every site host on a hosted-events instance is a
// team's), through the one function (sites.TeamSiteHost) that knows the
// format — hackhttp's own fallback (hack.go, used only when no SiteHandler
// is wired) calls the same function, so the format never scatters.
func (h *SiteHandler) TeamSiteURL(eventSlug, teamSlug string) string {
	return h.Hosts.Addressing().TeamSiteURL(eventSlug, teamSlug)
}

func (h *SiteHandler) TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (model.TeamSite, model.Site, error) {
	site, err := h.SitesSvc().GetSiteByUser(ctx, accountID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TeamSite{}, model.Site{}, nil
	}
	if err != nil {
		return model.TeamSite{}, model.Site{}, err
	}
	return model.TeamSite{Exists: true, ActiveVersion: site.ActiveVersion, Suspended: site.Suspended()}, site, nil
}

// TeamPreviewLink mints a preview address for version n of the team's site
// (the deadline version for judges). ok=false when there is none.
func (h *SiteHandler) TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool) {
	if n < 1 || !h.Hosts.Addressing().SiteCertReadyOn(eventSlug, h.Hosts.Addressing().HandoutBase()) {
		return "", time.Time{}, false
	}
	site, exists, err := h.exports().PreviewSite(ctx, export.PreviewSiteInput{AccountID: accountID, Name: teamSlug, Version: n})
	if err != nil || !exists {
		return "", time.Time{}, false
	}

	site.OwnerHandle = eventSlug
	return h.PreviewLink(site, n)
}

// TeamPreviewLinkFor is TeamPreviewLink for a site already read; the caller checked the version exists.
func (h *SiteHandler) TeamPreviewLinkFor(site model.Site, eventSlug string, n int) (string, bool) {
	if n < 1 || !h.Hosts.Addressing().SiteCertReadyOn(eventSlug, h.Hosts.Addressing().HandoutBase()) {
		return "", false
	}
	site.OwnerHandle = eventSlug
	link, _, ok := h.PreviewLink(site, n)
	return link, ok
}

func (h *SiteHandler) SuspendHeldSite(ctx context.Context, in sites.HeldSiteSuspension) error {
	return h.SitesSvc().SuspendHeldSite(ctx, in)
}

func (h *SiteHandler) TrashTeamSite(ctx context.Context, accountID, name string) error {
	return h.LifecycleSvc().TrashTeamSite(ctx, accountID, name)
}

// RemoveAccountFiles deletes a removed event's holding account folder (its
// team sites' files; the database rows went with the account).
func (h *SiteHandler) RemoveAccountFiles(userID, handle string) error {
	return h.Disk.DeleteUser(userID, handle)
}

// SyncAccountMarkers brings the take-down markers of every site of an
// account in line with the database (an event taken down or restored by the
// platform suspends its holding account: every team site goes down with it).
func (h *SiteHandler) SyncAccountMarkers(ctx context.Context, accountID string) error {
	sites, err := h.SitesSvc().ListSitesByUser(ctx, accountID)
	if err != nil {
		return err
	}
	var first error
	for _, s := range sites {
		if err := h.SitesSvc().SyncSiteSuspensionMarker(s); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// TeamSitesFrameBase is the domain team sites are served under
// (<team>.<event>.<base>, or the flat <team>--<event>.<base> single label
// under TEAM_SITE_FLAT_HOSTS — either way one extra label under base, which
// the judge route's "https://*.<base>" frame-src already matches), for the
// judge route's frame-src; "" when sites have no hosts of their own, so
// nothing is ever framed.
func (h *SiteHandler) TeamSitesFrameBase() string {
	if !h.Hosts.Addressing().SiteHostsOn() {
		return ""
	}
	return h.Hosts.Addressing().HandoutBase()
}
