package handler

import (
	"context"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/app/accounts"
	appstorage "github.com/vsriram/simple-host/host/app/storage"
)

// Storage access (owner decisions 2026-11-19: rules replace presets, for
// every kind). A key-value namespace, SQLite database or file bucket is
// governed by its rules, checked in the storage service (appstorage
// authorize, fileRule).
//
// The owner's tools (their key, the connector) always have full control.
// The site's owner signed in on the site's own address through visitor
// sign-in (D1) is the auth.role "owner" of the rules: see storageOwnerOnSite.

var storageTableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// storageResourceRulesJSON is a resource as the owner's tools see it: the
// rules that govern it (the owner-only set when none are stored), their
// status ("invalid" when they no longer compile) and, for SQLite, its
// per-table rules.
func storageResourceRulesJSON(x storageResource, diagnostics []httpx.RuleError) map[string]any {
	if diagnostics == nil {
		diagnostics = []httpx.RuleError{}
	}
	status := x.RulesStatus()
	if len(diagnostics) > 0 {
		status = "invalid" // stored rules that no longer compile refuse every visitor
	}
	out := map[string]any{"name": x.Name, "kind": x.Kind, "site_passcode": x.SitePasscode, "rules": x.EffectiveRules(""), "rules_status": status, "rules_errors": diagnostics}
	if x.Kind == "sqlite" {
		tables := map[string]any{}
		if x.Rules != nil {
			for name, table := range x.Tables {
				tables[name] = map[string]any{"rules": table.Rules}
			}
		}
		out["tables"] = tables
	}
	return out
}

func (h *SiteHandler) loadStorageResource(ctx context.Context, siteID, name string) (storageResource, error) {
	return h.storageSvc().LoadResource(ctx, appstorage.ResourceReadInput{SiteID: siteID, Name: name})
}

// storageOwnerOnSite reports whether a visitor session is the site's owner
// signed in on the site's own address (decision D1, 2026-10-10). Then the
// page acts with owner rights for storage data only (never settings,
// deploys, domains, passcodes, viewers, keys, versions or deleting the site;
// those routes require the owner's key). Conditions: the session was found by
// strictVisitorSession (same-origin, the __Host- cookie, bound to this site
// and this host); the account is the site's owner by id, so the email it
// signed in with is the account's own; the host is one of the site's own
// hosts (its site host, a family address, or its own domain), never the
// person host, the apex or the shared content host.
//
// On Simple Hack the owner is people, not the holding account (owner
// decision 2026-10-10): on a team site, an approved member of that team
// while the team may still change its site; on an event's custom website,
// an organiser of the event while it is neither ended nor taken down
// (the product's storage bindings provider). After the deadline a member is
// an ordinary visitor there.
//
// Never from a page framed by another origin (auth.js sends X-SH-Framed: 1
// there; it covers custom domains and families, whose pages nginx serves
// without ownerFrameGuard) or from a preview of an earlier version.
func (h *SiteHandler) storageOwnerOnSite(r *http.Request, c storageCall) bool {
	if c.resource.BeforePresets {
		return false // a resource saved before presets never had an owner on the site
	}
	return h.storageSiteOwnerOnSite(r, c)
}

// storageSiteOwnerOnSite is storageOwnerOnSite without the one-resource
// check, for a site-level storage endpoint that names no resource
// (the people pick list, SIM-358).
func (h *SiteHandler) storageSiteOwnerOnSite(r *http.Request, c storageCall) bool {
	if c.visitorAI || c.visitorID == "" {
		return false
	}
	if r.Header.Get("X-SH-Framed") != "" || h.previewRefererFor(r, c.siteID) {
		return false
	}
	if !h.storageOwnHost(r, c.siteID) {
		return false
	}
	// Simple Host: the account itself or a member of the team that holds
	// the site (accounts.ActsAsOwner, through the default provider).
	ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), c.siteID, c.visitorID)
	return err == nil && ok
}

// ownerFrameGuard: a page the site's owner opens while signed in on this
// host is never shown inside another origin's frame. There the page acts
// with owner rights for saved data (storageOwnerOnSite), and every
// <site>.<handle>.simple-host.app host is one "site" to a browser, so the
// Lax session cookie goes along when a sibling site frames it (clickjacking).
// Other visitors' pages are unchanged.
//
// On Simple Hack the owner is the team's members or the event's organisers
// (storageOwnerOnSite), and sibling team sites share one registrable domain
// the same way.
func (h *SiteHandler) ownerFrameGuard(w http.ResponseWriter, r *http.Request, ownerID string) {
	// A copy cached without the cookie must not stand in for one with it.
	w.Header().Add("Vary", "Cookie")
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "empty", "image", "script", "style", "font", "audio", "video", "track", "manifest", "worker", "sharedworker", "serviceworker":
		return // never framed (empty: fetch and XHR); no database lookup per subresource
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return
	}
	sess, err := h.storeAccounts().GetVisitorSession(r.Context(), id, h.limits.KeyIdleExpiry)
	if err != nil || !strings.EqualFold(sess.Host, requestHostName(r)) || time.Now().After(sess.ExpiresAt) {
		return
	}
	if h.ed.Storage().EventOwners {
		paperRecorder(r.Context()).Member(r.Context(), sess.UserID)
		if ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err != nil || !ok {
			return
		}
	} else if sess.UserID != ownerID {
		// A member of the team that holds the site is an owner here too
		// (accounts.ActsAsOwner); a failed lookup keeps the frame guard.
		if ok, err := accounts.ActsAsOwner(r.Context(), h.database, sess.UserID, ownerID); err == nil && !ok {
			return
		}
	}
	setOwnerFrameHeaders(w)
}

func setOwnerFrameHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
	}
}

func (h *SiteHandler) storageOwnHost(r *http.Request, siteID string) bool {
	host := requestHostName(r)
	if host == "" || h.isVisitorApexHost(host) || strings.EqualFold(host, h.contentHost) {
		return false
	}
	// On Simple Hack the event's host is the holding account's person
	// host, and it serves the event's custom website.
	if _, person := h.personHostOwner(r.Context(), host); person && !h.ed.Storage().EventOwners {
		return false
	}
	id, err := h.resolveSiteIDScoped(r, r.PathValue("sitename"))
	return err == nil && id == siteID
}

// storageVisitorActive refuses a suspended visitor account and keeps the
// session alive.
func (h *SiteHandler) storageVisitorActive(w http.ResponseWriter, r *http.Request, c *storageCall) bool {
	if c.activeChecked {
		return true
	}
	suspended, err := h.storeAccounts().UserSuspended(r.Context(), c.visitorSession.UserID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return false
	}
	if suspended {
		writeAccountSuspended(w)
		return false
	}
	_ = h.storeAccounts().TouchVisitorSession(r.Context(), c.visitorSession.ID, h.limits.VisitorSessionIdle)
	c.activeChecked = true
	return true
}
