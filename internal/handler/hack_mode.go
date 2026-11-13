package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/accounts"
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
