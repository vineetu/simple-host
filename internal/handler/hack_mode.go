package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

// Hosted events (EVENTS=hosted, simple-hack.app; docs/designs/simple-hack-platform.md).
//
// The same binary, run as the hackathon platform, differs from Simple Host in
// a few fixed places, each read from the injected *edition.Edition (its Kind
// and Presentation().HackAssets, and for the asset/chrome pipeline, its
// construction-time instance address; internal/edition):
//
//   - <handle>.<SITE_DOMAIN> answers only for an event's holding account, and
//     only with the server-rendered event page at "/" and a team's gallery
//     screenshot at /screenshots/<team> (serveHackEventHost); every other
//     single-label name is our 404, never the legacy redirect.
//   - Accounts own no personal sites: a site is a team's, of the event's
//     holding account, made only with a team credential (hack_sites.go).
//   - An account's handle is never chosen or changed by its person: it is a
//     random u-<hex> label, so sign-ups cannot squat event names.
//   - Sign-in email says Simple Hack.
//
// hackHandle is a new account's handle on the hackathon platform: a random
// label nobody picks, so it can never be an event's name.
func hackHandle() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return "u-" + hex.EncodeToString(b[:])
}

// SetHackEventPage sets what renders an event's page on its host: fn writes
// the page and reports true when user is an event's holding account.
func (h *SiteHandler) SetHackEventPage(fn func(w http.ResponseWriter, r *http.Request, user model.User) bool) {
	h.hackEventPage = fn
}

// SetHackScreenshot sets what serves GET/HEAD /screenshots/<team> on an event
// host: fn writes the image and reports true when that team is on the gallery.
func (h *SiteHandler) SetHackScreenshot(fn func(w http.ResponseWriter, r *http.Request, user model.User, team string) bool) {
	h.hackScreenshot = fn
}

// serveHackEventHost answers <event>.<SITE_DOMAIN> in hosted mode. A custom
// event site stays on this origin; the signed-in app and every API stay apex-only.
func (h *SiteHandler) serveHackEventHost(w http.ResponseWriter, r *http.Request, user model.User, api http.Handler) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		if h.hackEventAPIAllowed(r, user) {
			api.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ev, err := h.storeAccounts().GetEventByAccount(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderHackNotFound(w, r)
		} else {
			h.renderServiceError(w, r)
		}
		return
	}
	if ev.WebsiteMode == "custom" && !ev.TakenDown() {
		if _, err := h.opsSitesSvc().GetSiteByUser(r.Context(), ev.AccountID, ev.ID); err == nil {
			h.serveSiteFileRel(w, r, ev.AccountID, ev.ID, r.URL.Path)
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
			return
		}
	}
	if r.URL.Path == "/" && h.hackEventPage != nil && h.hackEventPage(w, r, user) {
		return
	}
	if slug, ok := hackSitesScreenshotPath(r.URL.Path); ok && h.hackScreenshot != nil && h.hackScreenshot(w, r, user, slug) {
		return
	}
	h.renderHackNotFound(w, r)
}

// Only the custom event website's own storage and visitor sign-in endpoints
// are served on its origin. Event management remains on the trusted apex.
func (h *SiteHandler) hackEventAPIAllowed(r *http.Request, owner model.User) bool {
	ev, err := h.storeAccounts().GetEventByAccount(r.Context(), owner.ID)
	if err != nil || ev.WebsiteMode != "custom" || ev.TakenDown() {
		return false
	}
	if _, err := h.opsSitesSvc().GetSiteByUser(r.Context(), owner.ID, ev.ID); err != nil {
		return false
	}
	p := r.URL.Path
	base := "/v1/sites/" + ev.Slug
	if p == base+"/me" || p == base+"/visitor/auth" || p == base+"/visitor/auth/verify" ||
		strings.HasPrefix(p, base+"/storage/") {
		return true
	}
	return p == "/v1/sso/start" || p == "/v1/visitor/establish" || p == "/v1/visitor/logout" ||
		strings.HasPrefix(p, "/v1/visitor/oauth/")
}

// renderHackNotFound is the platform's 404 on an event or unknown host.
func (h *SiteHandler) renderHackNotFound(w http.ResponseWriter, r *http.Request) {
	h.renderNotFoundPage(w, r,
		"Event not found",
		"No event or page lives at this address.",
		h.mainSiteURL(), "Go to "+h.siteDomain)
}

func HackAccountDeleteBlock(ctx context.Context, q accounts.DeletionQueries, userID string, holdingAccounts bool) (string, string, error) {
	return accounts.CheckHackAccountDeletion(ctx, q, userID, holdingAccounts)
}

func HackHoldingAccount(ctx context.Context, q accounts.DeletionQueries, userID string, holdingAccounts bool) (bool, error) {
	return accounts.HoldingAccount(ctx, q, userID, holdingAccounts)
}

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
		if h.teamSiteFlatHostsOn() && h.flatLabelConfirmedNotAHandle(ctx, label) {
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

// hackSitesGallerySlug is hack_wire.go's hackGallerySlug: one label,
// lowercase letters, digits and hyphens, at most 30 characters, not
// starting or ending with a hyphen.
var hackSitesGallerySlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func hackSitesGallerySlug(s string) bool {
	return len(s) <= 30 && hackSitesGallerySlugRe.MatchString(s)
}

// hackSitesScreenshotPath is hack_wire.go's hackScreenshotPath, kept here
// for hack_mode.go's event-host routing, which needs it to recognize
// /screenshots/<team> before dispatching to the Hack-side hook.
func hackSitesScreenshotPath(p string) (string, bool) {
	const prefix = "/screenshots/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	slug := p[len(prefix):]
	if !hackSitesGallerySlug(slug) {
		return "", false
	}
	return slug, true
}
