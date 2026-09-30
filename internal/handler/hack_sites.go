package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
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
func hackNoPersonalSites(w http.ResponseWriter, user *db.User) bool {
	if !hackMode || user.IsAdmin || user.Team != nil {
		return true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "sites on simple-hack.app belong to an event's teams: publish with your team key from your event page on simple-hack.app",
		Code:  "no_personal_sites",
	})
	return false
}

// hackTeamTxGate runs inside a deploy, upload or rollback transaction: for a
// team credential it checks the site is the team's, the event's certificate
// is ready, and the team may still change its site, holding the team row FOR
// SHARE until the transaction ends. false after writing the answer. Anyone
// else passes.
func (h *SiteHandler) hackTeamTxGate(w http.ResponseWriter, r *http.Request, tx *sql.Tx, user *db.User, siteName string) bool {
	if !hackMode || user.Team == nil {
		return true
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this team key works on your team's site only: " + user.Team.TeamSlug, Code: "team_site_only"})
		return false
	}
	st, err := db.TeamWriteStateFor(r.Context(), tx, user.Team.TeamID, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "this team key stopped working: its team is gone", Code: "team_key_inactive"})
			return false
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
		writeJSON(w, status, errorResponse{Error: msg, Code: code})
		return false
	}
	if !h.teamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			h.RequestSiteCert(user.Handle.String)
		}
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "your event's team addresses are still being set up; try again in a few minutes",
			Code:  "team_sites_not_ready",
		})
		return false
	}
	return true
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

// TeamSite is a team site's state as the events API shows it.
type TeamSite struct {
	Exists        bool
	ActiveVersion int
	UpdatedAt     time.Time
	Suspended     bool
}

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
	if err := h.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM versions WHERE site_id = $1 AND version_number = $2 AND status <> 'uploading')`, site.ID, n).Scan(&exists); err != nil || !exists {
		return "", time.Time{}, false
	}
	site.OwnerHandle = eventSlug
	return h.previewLink(site, n)
}

// hackOrganiserTakedownPrefix starts the take-down reason an organiser's
// take-down puts on a team's site; the organiser can undo only those, never
// the platform admin's.
const hackOrganiserTakedownPrefix = "Taken down by the event organiser"

// ErrNotOrganiserTakedown: the site is down by the platform admin's hand.
var ErrNotOrganiserTakedown = errors.New("taken down by the platform")

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
	if !hackMode {
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
	if !hackMode {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			h.hackSweep(ctx)
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
