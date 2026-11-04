package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

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

// hackNoPersonalSites refuses a new site on the hackathon platform unless the
// caller is a team credential (or the admin); false after writing the answer.
func (h *SiteHandler) hackNoPersonalSites(w http.ResponseWriter, user *model.User) bool {
	if h.ed.Identity().PersonalSites || user.IsAdmin || user.Team != nil || user.EventWebsite != nil {
		return true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "team sites belong to an event: select your team in the Simple Hack connection before publishing",
		Code:  "no_personal_sites",
	})
	return false
}

// teamSitesReady: *.<event>.<SITE_DOMAIN> is served with its certificate. A
// pure check: certificates are asked for only for events that are open,
// building or closed (HackHandler.requestEventCert, the sweep, a deploy),
// never from a page view, so drafts and deleted events spend no budget. Team
// sites are offered and deployed only then, so a
// team's work never lands anywhere but its own origin.
//
// Under TEAM_SITE_FLAT_HOSTS, team sites live under the instance's own
// *.<base> wildcard instead of a per-event one, so readiness never waits on
// a per-event marker — RequestSiteCertContext (sitehost.go) never requests
// one either.
func (h *SiteHandler) teamSitesReady(eventSlug string) bool {
	if eventSlug == "" || !h.siteHostsOn() {
		return false
	}
	if h.teamSiteFlatHostsOn() {
		return true
	}
	return h.siteCertReady(eventSlug)
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

// SetTeamSiteFlatHosts sets TEAM_SITE_FLAT_HOSTS.
func (h *SiteHandler) SetTeamSiteFlatHosts(on bool) { h.teamSiteFlatHosts = on }

// TeamSiteURL is the address of a team's site: sites.TeamSiteHost is the one
// function that knows the format (the two-level <team>.<event>.<base>, or
// the flat <team>--<event>.<base> single label under
// TEAM_SITE_FLAT_HOSTS) — hackhttp's own fallback (hack.go, used only when
// no SiteHandler is wired) calls the same function, so the format never
// scatters across the two packages.
func (h *SiteHandler) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + sites.TeamSiteHost(eventSlug, teamSlug, h.handoutBase(), h.teamSiteFlatHostsOn()) + "/"
}

// PublishEventWebsite sends an organiser's upload through the same validated,
// versioned deploy path as a team site. The holding account is visible only to
// this scoped internal request; the transaction gate rechecks membership at commit.
func (h *SiteHandler) PublishEventWebsite(w http.ResponseWriter, r *http.Request, ev model.Event, organiserID string, files bool) {
	if r.URL.Query().Get("publish") == "false" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "event website uploads go live when published")
		return
	}
	holder, err := h.storeAccounts().GetUserByID(r.Context(), ev.AccountID)
	if err != nil || holder.Suspended {
		hackSitesEventNotFound(w)
		return
	}
	holder.EventWebsite = &model.EventWebsiteIdentity{EventID: ev.ID, EventSlug: ev.Slug, OrganiserID: organiserID}
	r = r.WithContext(auth.WithUser(r.Context(), &holder))
	r.SetPathValue("sitename", ev.ID)
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

// hackPinnedFloor lowers a prune threshold so a team's deadline version is
// kept whatever the retention.
func (h *SiteHandler) hackPinnedFloor(ctx context.Context, userID, siteName string, keepFrom int) int {
	if !h.ed.Identity().TeamKeys {
		return keepFrom
	}
	v, ok, err := h.hackSiteSvc().SitePinnedVersion(ctx, userID, siteName)
	if err != nil {
		log.Printf("prune versions for %s: pinned version: %v", siteName, err)
		return 0 // keep everything rather than guess
	}
	if ok && v < keepFrom {
		return v
	}
	return keepFrom
}

// StartHackSweep pins teams whose deadline has passed and moves sites whose
// team is gone to Recently deleted, now and then every interval.
func (h *SiteHandler) StartHackSweep(ctx context.Context, every time.Duration, alsoFn func(context.Context)) {
	o := h.siteLoop("sites.hack_sweep")
	o.once.Do(func() {
		if o.enabled() {
			h.startHackSweep(ctx, every, alsoFn)
		}
	})
}
func (h *SiteHandler) startHackSweep(ctx context.Context, every time.Duration, alsoFn func(context.Context)) {
	if !h.ed.Identity().TeamKeys {
		return
	}
	h.siteLoop("sites.hack_sweep").Go(ctx, func(ctx context.Context) {
		ctx = sites.WithWorkerReport(ctx, h.siteLoop("sites.hack_sweep").tracker)
		t := time.NewTicker(every)
		defer t.Stop()
		for n := 0; ; n++ {
			h.siteLoop("sites.hack_sweep").tracker.Attempt()
			h.hackSweep(ctx)
			// Take-down markers follow the database again every ten rounds
			// (a marker write that failed after a take-down is retried).
			if n%10 == 9 {
				h.SyncSuspendMarkers(ctx)
			}
			if alsoFn != nil {
				alsoFn(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

// HackSweepForTest is hackSweep, exported as a test hook (cleanup 3.2 slice
// 3): the moved hack_m2_test.go runs one sweep on demand
// (TestHackM2ReviewRound2) instead of waiting on StartHackSweep's ticker.
func (h *SiteHandler) HackSweepForTest(ctx context.Context) { h.hackSweep(ctx) }

func (h *SiteHandler) hackSweep(ctx context.Context) {
	h.hackSiteSvc().SweepSites(ctx, h.TrashTeamSite, func() { sites.ReportWorkerProgress(ctx) }, func(op string, err error) { sites.ReportWorkerFailure(ctx, op, err) })
}

// hackTLSHostExists bounds on-demand certificates to actual platform names.
// A real archived or taken-down host still needs HTTPS to show its page or
// notice; arbitrary names and deleted projects must not consume certificates.
// Used by the Caddy on-demand TLS gate (domains.go tlsAsk); a Kubernetes
// install with a static *.<base> wildcard never calls this.
func (h *SiteHandler) hackTLSHostExists(ctx context.Context, host string) bool {
	if host == strings.ToLower(h.contentHost) {
		return true
	}
	for _, base := range h.servedBases() {
		if host == base || host == "www."+base {
			return true
		}
		if !strings.HasSuffix(host, "."+base) {
			continue
		}
		label := strings.TrimSuffix(host, "."+base)
		if h.teamSiteFlatHostsOn() {
			if team, event, ok := sites.TeamSiteHostLabels(label); ok {
				return h.hackTeamSiteExists(ctx, event, team)
			}
		}
		labels := strings.Split(label, ".")
		if len(labels) < 1 || len(labels) > 2 {
			return false
		}
		if _, err := h.hackSiteSvc().SiteEventBySlug(ctx, labels[len(labels)-1]); err != nil {
			return false
		}
		if len(labels) == 1 {
			return true
		}
		return h.hackTeamSiteExists(ctx, labels[1], labels[0])
	}
	return false
}

// hackTeamSiteExists reports whether team is a real team of event with a
// live site: the on-demand TLS allowlist's team-site check, shared by the
// two-level (<team>.<event>.<base>) and flat (<team>--<event>.<base>) host
// shapes.
func (h *SiteHandler) hackTeamSiteExists(ctx context.Context, eventSlug, teamSlug string) bool {
	ev, err := h.hackSiteSvc().SiteEventBySlug(ctx, eventSlug)
	if err != nil {
		return false
	}
	if _, err := h.hackSiteSvc().TeamBySlug(ctx, ev.ID, teamSlug); err != nil {
		return false
	}
	site, err := h.sitesSvc().GetSiteByUser(ctx, ev.AccountID, teamSlug)
	return err == nil && site.ActiveVersion > 0
}

func (h *SiteHandler) PublishTeamArchive(w http.ResponseWriter, r *http.Request, member model.User, teamID string) {
	id, err := h.hackSiteSvc().SiteTeamIdentity(r.Context(), member.ID, teamID)
	if err != nil {
		hackSitesEventNotFound(w)
		return
	}
	holder, err := h.storeAccounts().GetUserByID(r.Context(), id.AccountID)
	if err != nil || holder.Suspended {
		hackSitesEventNotFound(w)
		return
	}
	holder.IsAdmin = false
	holder.Team = &id
	r = r.WithContext(auth.WithUser(r.Context(), &holder))
	r.SetPathValue("sitename", id.TeamSlug)
	st, err := h.hackSiteSvc().WriteState(r.Context(), teamID)
	if err != nil {
		writeInternal(w)
		return
	}
	if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
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
