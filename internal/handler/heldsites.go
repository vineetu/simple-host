package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

// GroupRef identifies a group and its holding account.
type GroupRef struct{ ID, Slug, AccountID string }

// Team sites on the hackathon platform (EVENTS=hosted, M2;
// docs/designs/simple-hack-platform.md).
//
// A team's site is the site named after the team of the event's holding
// account, served at <team>.<event>.<SITE_DOMAIN> once *.<event>.<SITE_DOMAIN>
// has its certificate. Only a team credential (a member's team key, or a
// connector connection bound to the team) acts on it, and only on that one
// site (auth.ScopeGate holds the routes; these checks hold the handlers).
// Every deploy, upload and rollback checks the team's deadline inside its
// transaction, holding the team row FOR SHARE, so the deadline pin
// (db.PinDueTeams) sees exactly what was let in before the deadline.

// teamSitesReady: *.<event>.<SITE_DOMAIN> is served with its certificate. A
// pure check: certificates are asked for only for events that are open,
// building or closed (HackHandler.requestEventCert, the sweep, a deploy),
// never from a page view, so drafts and deleted events spend no budget. Team
// sites are offered and deployed only then, so a
// team's work never lands anywhere but its own origin.
//
// siteCertReady (sitebase.go's siteCertReadyOn) is itself flat-aware: under
// TEAM_SITE_FLAT_HOSTS it never waits on a per-event marker, since team
// sites live under the instance's own *.<base> wildcard instead.
func (h *SiteHandler) teamSitesReady(eventSlug string) bool {
	return eventSlug != "" && h.siteHostsOn() && h.siteCertReady(eventSlug)
}

// TeamSitesReady is teamSitesReady for the events API.
func (h *SiteHandler) TeamSitesReady(eventSlug string) bool { return h.teamSitesReady(eventSlug) }

// teamSiteFlatHostsOn: TEAM_SITE_FLAT_HOSTS is on, for an instance running
// hosted events — the only edition team sites exist in. Off on every other
// edition, and off by default on simple-hack.app itself, so the two-level
// form stays byte-for-byte unchanged unless an operator turns this on.
func (h *SiteHandler) teamSiteFlatHostsOn() bool {
	return h.teamSiteFlatHosts && h.ed.Routes().HostedEvents
}

// SetTeamSiteFlatHosts sets TEAM_SITE_FLAT_HOSTS, including the served
// auth.js patch (basetext.go) that teaches it to take the part of a flat
// team-site host's one label before "--" as the site name.
func (h *SiteHandler) SetTeamSiteFlatHosts(on bool) {
	h.teamSiteFlatHosts = on
	setTeamSiteFlatHostsText(on && h.ed.Routes().HostedEvents)
}

// TeamSiteFlatHostsOn is teamSiteFlatHostsOn for hackhttp (hackSiteHooks).
func (h *SiteHandler) TeamSiteFlatHostsOn() bool { return h.teamSiteFlatHostsOn() }

// TeamSiteURL is the address of a team's site: siteHostFor (sitehost.go) is
// itself flat-aware (every site host on a hosted-events instance is a
// team's), through the one function (sites.TeamSiteHost) that knows the
// format — hackhttp's own fallback (hack.go, used only when no SiteHandler
// is wired) calls the same function, so the format never scatters.
func (h *SiteHandler) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + h.siteHostFor(eventSlug, teamSlug) + "/"
}

// PublishEventWebsite sends an organiser's upload through the same validated,
// versioned deploy path as a team site. The holding account is visible only to
// this scoped internal request; the transaction gate rechecks membership at commit.
func (h *SiteHandler) PublishEventWebsite(w http.ResponseWriter, r *http.Request, g GroupRef, organiserID string, files bool) {
	if r.URL.Query().Get("publish") == "false" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "event website uploads go live when published")
		return
	}
	holder, err := h.storeAccounts().GetUserByID(r.Context(), g.AccountID)
	if err != nil || holder.Suspended {
		heldSiteNotFound(w)
		return
	}
	holder.EventWebsite = &model.EventWebsiteIdentity{EventID: g.ID, EventSlug: g.Slug, OrganiserID: organiserID}
	r = r.WithContext(auth.WithUser(r.Context(), &holder))
	r.SetPathValue("sitename", g.ID)
	if files {
		h.updateSiteFiles(w, r)
	} else {
		h.updateSite(w, r)
	}
}

// TeamSite is a team site's state as the events API shows it. Moved to
// model.TeamSite (cleanup 3.2 slice 1c) so the future internal/hackhttp
// package can read it without importing handler; this alias keeps every
// staying caller unchanged.
//
// Transitional (cleanup 3.2): callers move to model.TeamSite directly in
// a later slice.
type TeamSite = model.TeamSite

// TeamSiteInfo reads the team's site of the holding account, if any.
func (h *SiteHandler) TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (TeamSite, model.Site, error) {
	site, err := h.sitesSvc().GetSiteByUser(ctx, accountID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamSite{}, model.Site{}, nil
	}
	if err != nil {
		return TeamSite{}, model.Site{}, err
	}
	return TeamSite{Exists: true, ActiveVersion: site.ActiveVersion, Suspended: site.Suspended()}, site, nil
}

// TeamPreviewLink mints a preview address for version n of the team's site
// (the deadline version for judges). ok=false when there is none.
func (h *SiteHandler) TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool) {
	if n < 1 || !h.siteCertReady(eventSlug) {
		return "", time.Time{}, false
	}
	site, exists, err := h.lifecycleSvc().PreviewSite(ctx, sites.PreviewSiteInput{AccountID: accountID, Name: teamSlug, Version: n})
	if err != nil || !exists {
		return "", time.Time{}, false
	}

	site.OwnerHandle = eventSlug
	return h.previewLink(site, n)
}

// ErrNotOrganiserTakedown moved to model.ErrNotOrganiserTakedown (cleanup
// 3.2 slice 1c); alias kept so this file's own callers are unchanged.
//
// Transitional (cleanup 3.2): callers move to model.ErrNotOrganiserTakedown
// directly in a later slice.
var ErrNotOrganiserTakedown = model.ErrNotOrganiserTakedown

// TeamPreviewLinkFor mints a preview address for version n of a team site
// already read (the caller checked the version exists). No database work.
func (h *SiteHandler) TeamPreviewLinkFor(site model.Site, eventSlug string, n int) (string, bool) {
	if n < 1 || !h.siteCertReady(eventSlug) {
		return "", false
	}
	site.OwnerHandle = eventSlug
	link, _, ok := h.previewLink(site, n)
	return link, ok
}

// SetTeamSiteTakenDown takes a team's site down for the organiser, or brings
// it back: the team row records it (deploys and entry edits stop) and the
// site, when there is one, gets the site take-down (sites.suspended_at), so
// every address shows the take-down page and saves stop. One transaction,
// site row before team row (the order a deploy takes them).
func (h *SiteHandler) SetTeamSiteTakenDown(ctx context.Context, accountID, teamID, teamSlug string, on bool, reason string) error {
	return h.sitesSvc().SetTeamSiteTakenDown(ctx, sites.TeamSiteTakedownInput{
		AccountID: accountID, TeamID: teamID, TeamSlug: teamSlug, On: on, Reason: reason,
	})
}

// TrashTeamSite moves the holding account's site of that name to Recently
// deleted (its team is gone). Nothing when there is none.
func (h *SiteHandler) TrashTeamSite(ctx context.Context, accountID, name string) error {
	unlock := h.lockSite(accountID, name)
	defer unlock()
	site, err := h.sitesSvc().GetSiteByUser(ctx, accountID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = h.trashSite(ctx, site, nil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// RemoveAccountFiles deletes a removed event's holding account folder (its
// team sites' files; the database rows went with the account).
func (h *SiteHandler) RemoveAccountFiles(userID, handle string) error {
	return h.disk.DeleteUser(userID, handle)
}

// SyncAccountMarkers brings the take-down markers of every site of an
// account in line with the database (an event taken down or restored by the
// platform suspends its holding account: every team site goes down with it).
func (h *SiteHandler) SyncAccountMarkers(ctx context.Context, accountID string) error {
	sites, err := h.sitesSvc().ListSitesByUser(ctx, accountID)
	if err != nil {
		return err
	}
	var first error
	for _, s := range sites {
		if err := h.syncSiteMarker(s); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (h *SiteHandler) PublishTeamArchive(w http.ResponseWriter, r *http.Request, id model.TeamIdentity, refusal func(context.Context) (status int, code, msg string, err error)) {
	holder, err := h.storeAccounts().GetUserByID(r.Context(), id.AccountID)
	if err != nil || holder.Suspended {
		heldSiteNotFound(w)
		return
	}
	holder.IsAdmin = false
	holder.Team = &id
	r = r.WithContext(auth.WithUser(r.Context(), &holder))
	r.SetPathValue("sitename", id.TeamSlug)
	status, code, msg, err := refusal(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	if code != "" {
		writeJSON(w, status, errorResponse{Error: msg, Code: code})
		return
	}
	rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSite)).ServeHTTP(w, r)
}

// TeamSitesFrameBase is the domain team sites are served under
// (<team>.<event>.<base>, or the flat <team>--<event>.<base> single label
// under TEAM_SITE_FLAT_HOSTS — either way one extra label under base, which
// the judge route's "https://*.<base>" frame-src already matches), for the
// judge route's frame-src; "" when sites have no hosts of their own, so
// nothing is ever framed.
func (h *SiteHandler) TeamSitesFrameBase() string {
	if !h.siteHostsOn() {
		return ""
	}
	return h.handoutBase()
}

// heldSiteNotFound preserves the event API response for an unavailable holding account.
func heldSiteNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "event not found", Code: "event_not_found"})
}
