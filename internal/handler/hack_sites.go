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
	db "github.com/vsriram/simple-host/internal/db"
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
func (h *SiteHandler) hackNoPersonalSites(w http.ResponseWriter, user *db.User) bool {
	if h.ed.Identity().PersonalSites || user.IsAdmin || user.Team != nil || user.EventWebsite != nil {
		return true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "team sites belong to an event: select your team in the Simple Hack connection before publishing",
		Code:  "no_personal_sites",
	})
	return false
}

func (h *SiteHandler) hackTeamTxDecision(ctx context.Context, tx *sql.Tx, user *db.User, siteName string, paperSite func(), paperState func(string)) *sites.GateError {
	if h.ed.Identity().TeamKeys && user.EventWebsite != nil {
		id := user.EventWebsite
		if siteName != id.EventID || user.ID == "" {
			return &sites.GateError{Code: "event_not_found", Message: "event not found"}
		}
		var role, stage string
		var takenDown bool
		err := tx.QueryRowContext(ctx, `SELECT m.role, e.stage, e.taken_down_at IS NOT NULL
			FROM events e JOIN event_members m ON m.event_id = e.id
			WHERE e.id = $1 AND e.slug = $2 AND e.account_id = $3 AND m.user_id = $4
			FOR SHARE OF e, m`, id.EventID, id.EventSlug, user.ID, id.OrganiserID).Scan(&role, &stage, &takenDown)
		if err != nil || role != "organiser" {
			return &sites.GateError{Code: "event_not_found", Message: "event not found"}
		}
		if takenDown {
			return &sites.GateError{Code: "event_taken_down", Message: "this event has been taken down"}
		}
		if stage == "archived" {
			return &sites.GateError{Code: "event_closed", Message: "this event has ended"}
		}
		return nil
	}
	if !h.ed.Identity().TeamKeys || user.Team == nil {
		return nil
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		return &sites.GateError{Message: "this team key works on your team's site only: " + user.Team.TeamSlug, Code: "team_site_only"}
	}
	var memberID string
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM event_members WHERE user_id = $1 AND team_id = $2 AND role = 'participant' AND approval_status = 'approved' FOR SHARE`, user.Team.MemberID, user.Team.TeamID).Scan(&memberID); err != nil {
		return &sites.GateError{Code: "event_not_found", Message: "event not found"}
	}
	paperSite()
	st, err := db.TeamWriteStateFor(ctx, tx, user.Team.TeamID, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &sites.GateError{Message: "this team key stopped working: its team is gone", Code: "team_key_inactive"}
		}
		return &sites.GateError{Message: "internal server error"}
	}
	paperState(st.Stage)
	if code := st.WriteRefusal(); code != "" {
		_, msg := auth.TeamRefusal(code)
		return &sites.GateError{Message: msg, Code: code}
	}
	if !h.teamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			h.RequestSiteCert(user.Handle.String)
		}
		return &sites.GateError{Message: "your event's team addresses are still being set up; try again in a few minutes", Code: "team_sites_not_ready"}
	}
	return nil
}

// teamSitesReady: *.<event>.<SITE_DOMAIN> is served with its certificate. A
// pure check: certificates are asked for only for events that are open,
// building or closed (HackHandler.requestEventCert, the sweep, a deploy),
// never from a page view, so drafts and deleted events spend no budget. Team
// sites are offered and deployed only then, so a
// team's work never lands anywhere but its own origin.
func (h *SiteHandler) teamSitesReady(eventSlug string) bool {
	return eventSlug != "" && h.siteHostsOn() && h.siteCertReady(eventSlug)
}

// TeamSitesReady is teamSitesReady for the events API.
func (h *SiteHandler) TeamSitesReady(eventSlug string) bool { return h.teamSitesReady(eventSlug) }

// TeamSiteURL is the address of a team's site.
func (h *SiteHandler) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + h.siteHostFor(eventSlug, teamSlug) + "/"
}

// PublishEventWebsite sends an organiser's upload through the same validated,
// versioned deploy path as a team site. The holding account is visible only to
// this scoped internal request; hackTeamTxDecision rechecks membership at commit.
func (h *SiteHandler) PublishEventWebsite(w http.ResponseWriter, r *http.Request, ev db.Event, organiserID string, files bool) {
	if r.URL.Query().Get("publish") == "false" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "event website uploads go live when published")
		return
	}
	holder, err := db.GetUserByID(r.Context(), h.database, ev.AccountID)
	if err != nil || holder.Suspended {
		hackSitesEventNotFound(w)
		return
	}
	holder.EventWebsite = &db.EventWebsiteIdentity{EventID: ev.ID, EventSlug: ev.Slug, OrganiserID: organiserID}
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
func (h *SiteHandler) TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (TeamSite, db.Site, error) {
	site, err := db.GetSiteByUser(ctx, h.database, accountID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return TeamSite{}, db.Site{}, nil
	}
	if err != nil {
		return TeamSite{}, db.Site{}, err
	}
	return TeamSite{Exists: true, ActiveVersion: site.ActiveVersion, Suspended: site.Suspended()}, site, nil
}

// TeamPreviewLink mints a preview address for version n of the team's site
// (the deadline version for judges). ok=false when there is none.
func (h *SiteHandler) TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool) {
	if n < 1 || !h.siteCertReady(eventSlug) {
		return "", time.Time{}, false
	}
	site, err := db.GetSiteByUser(ctx, h.database, accountID, teamSlug)
	if err != nil {
		return "", time.Time{}, false
	}
	var exists bool
	if err := h.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM versions WHERE site_id = $1 AND version_number = $2 AND status IN ('active', 'ready'))`, site.ID, n).Scan(&exists); err != nil || !exists {
		return "", time.Time{}, false
	}
	site.OwnerHandle = eventSlug
	return h.previewLink(site, n)
}

// hackOrganiserTakedownPrefix starts the take-down reason an organiser's
// take-down puts on a team's site; the organiser can undo only those, never
// the platform admin's.
const hackOrganiserTakedownPrefix = "Taken down by the event organiser"

// ErrNotOrganiserTakedown moved to model.ErrNotOrganiserTakedown (cleanup
// 3.2 slice 1c); alias kept so this file's own callers are unchanged.
//
// Transitional (cleanup 3.2): callers move to model.ErrNotOrganiserTakedown
// directly in a later slice.
var ErrNotOrganiserTakedown = model.ErrNotOrganiserTakedown

// TeamPreviewLinkFor mints a preview address for version n of a team site
// already read (the caller checked the version exists). No database work.
func (h *SiteHandler) TeamPreviewLinkFor(site db.Site, eventSlug string, n int) (string, bool) {
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
	unlock := h.lockSite(accountID, teamSlug)
	defer unlock()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var siteID, current string
	err = tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(suspended_reason, '') FROM sites
		 WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL FOR UPDATE`, accountID, teamSlug).Scan(&siteID, &current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if siteID != "" {
		switch {
		case on:
			full := hackOrganiserTakedownPrefix
			if reason != "" {
				full += ": " + reason
			}
			if current == "" || strings.HasPrefix(current, hackOrganiserTakedownPrefix) {
				if err := db.SetSiteSuspended(ctx, tx, siteID, full); err != nil {
					return err
				}
			}
		case current != "" && !strings.HasPrefix(current, hackOrganiserTakedownPrefix):
			return ErrNotOrganiserTakedown
		case current != "":
			if err := db.SetSiteSuspended(ctx, tx, siteID, ""); err != nil {
				return err
			}
		}
	}
	if on {
		_, err = tx.ExecContext(ctx, `UPDATE event_teams SET site_taken_down_at = COALESCE(site_taken_down_at, now()), site_taken_down_reason = $2 WHERE id = $1`, teamID, reason)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE event_teams SET site_taken_down_at = NULL, site_taken_down_reason = '' WHERE id = $1`, teamID)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if siteID != "" {
		fresh, err := db.GetSiteByID(ctx, h.database, siteID)
		if err != nil {
			return err
		}
		return h.syncSiteMarker(fresh)
	}
	return nil
}

// TrashTeamSite moves the holding account's site of that name to Recently
// deleted (its team is gone). Nothing when there is none.
func (h *SiteHandler) TrashTeamSite(ctx context.Context, accountID, name string) error {
	unlock := h.lockSite(accountID, name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, h.database, accountID, name)
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
	sites, err := db.ListSitesByUser(ctx, h.database, accountID)
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
	v, ok, err := db.PinnedVersionOfSite(ctx, h.database, userID, siteName)
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
	if !h.ed.Identity().TeamKeys {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for n := 0; ; n++ {
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
	}()
}

// HackSweepForTest is hackSweep, exported as a test hook (cleanup 3.2 slice
// 3): the moved hack_m2_test.go runs one sweep on demand
// (TestHackM2ReviewRound2) instead of waiting on StartHackSweep's ticker.
func (h *SiteHandler) HackSweepForTest(ctx context.Context) { h.hackSweep(ctx) }

func (h *SiteHandler) hackSweep(ctx context.Context) {
	if err := db.PinDueTeams(ctx, h.database, ""); err != nil {
		log.Printf("hack sweep: pin: %v", err)
	}
	if err := db.DeleteEmptyOpenTeams(ctx, h.database); err != nil {
		log.Printf("hack sweep: empty teams: %v", err)
	}
	orphans, err := db.OrphanTeamSites(ctx, h.database, "")
	if err != nil {
		log.Printf("hack sweep: orphan sites: %v", err)
		return
	}
	for _, s := range orphans {
		if err := h.TrashTeamSite(ctx, s.UserID, s.Name); err != nil {
			log.Printf("hack sweep: remove site %s of a removed team: %v", s.Name, err)
		} else {
			log.Printf("hack sweep: site %s of a removed team moved to Recently deleted", s.Name)
		}
	}
}

// hackTLSHostExists bounds on-demand certificates to actual platform names.
// A real archived or taken-down host still needs HTTPS to show its page or
// notice; arbitrary names and deleted projects must not consume certificates.
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
		labels := strings.Split(strings.TrimSuffix(host, "."+base), ".")
		if len(labels) < 1 || len(labels) > 2 {
			return false
		}
		ev, err := db.GetEventBySlug(ctx, h.database, labels[len(labels)-1])
		if err != nil {
			return false
		}
		if len(labels) == 1 {
			return true
		}
		if _, err := db.GetEventTeamBySlug(ctx, h.database, ev.ID, labels[0]); err != nil {
			return false
		}
		site, err := db.GetSiteByUser(ctx, h.database, ev.AccountID, labels[0])
		return err == nil && site.ActiveVersion > 0
	}
	return false
}

func (h *SiteHandler) PublishTeamArchive(w http.ResponseWriter, r *http.Request, member db.User, teamID string) {
	id, err := db.ResolveTeamIdentity(r.Context(), h.database, member.ID, teamID)
	if err != nil {
		hackSitesEventNotFound(w)
		return
	}
	holder, err := db.GetUserByID(r.Context(), h.database, id.AccountID)
	if err != nil || holder.Suspended {
		hackSitesEventNotFound(w)
		return
	}
	holder.IsAdmin = false
	holder.Team = &id
	r = r.WithContext(auth.WithUser(r.Context(), &holder))
	r.SetPathValue("sitename", id.TeamSlug)
	st, err := db.TeamWriteStateFor(r.Context(), h.database, teamID, false)
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
// (<team>.<event>.<base>), for the judge route's frame-src; "" when sites
// have no hosts of their own, so nothing is ever framed.
func (h *SiteHandler) TeamSitesFrameBase() string {
	if !h.siteHostsOn() {
		return ""
	}
	return h.handoutBase()
}
