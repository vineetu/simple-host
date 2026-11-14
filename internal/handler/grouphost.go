package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

// Group is the address and website state of a holding account's group.
type Group struct {
	GroupRef
	CustomWebsite bool
	TakenDown     bool
}

// GroupHosts answers for groups and their addresses. Nil on a platform-only install.
type GroupHosts interface {
	// ByAccount returns sql.ErrNoRows when the account holds no group.
	ByAccount(context.Context, string) (Group, error)
	// BySlug returns sql.ErrNoRows when no group has this address label.
	BySlug(context.Context, string) (Group, error)
	HasSite(ctx context.Context, groupID, siteName string) (bool, error)
	PinnedVersion(ctx context.Context, accountID, siteName string) (version int, ok bool, err error)
	// ServePage runs only for GET/HEAD, after the platform's custom website branch.
	ServePage(http.ResponseWriter, *http.Request, model.User) bool
	Texts() GroupTexts
}

type GroupTexts struct {
	NotFoundTitle, NotFoundBody string
	NoPersonalSites             string
}

func (h *SiteHandler) SetGroupHosts(hosts GroupHosts) { h.groupHosts = hosts }

func (h *SiteHandler) groupByAccount(ctx context.Context, id string) (Group, error) {
	if h.groupHosts == nil {
		return Group{}, sql.ErrNoRows
	}
	return h.groupHosts.ByAccount(ctx, id)
}
func (h *SiteHandler) groupBySlug(ctx context.Context, slug string) (Group, error) {
	if h.groupHosts == nil {
		return Group{}, sql.ErrNoRows
	}
	return h.groupHosts.BySlug(ctx, slug)
}
func (h *SiteHandler) groupTexts() GroupTexts {
	if h.groupHosts != nil {
		return h.groupHosts.Texts()
	}
	return GroupTexts{NotFoundTitle: "Page not found", NotFoundBody: "No page lives at this address."}
}

// serveGroupHost answers <event>.<SITE_DOMAIN> in hosted mode. A custom
// event site stays on this origin; the signed-in app and every API stay apex-only.
func (h *SiteHandler) serveGroupHost(w http.ResponseWriter, r *http.Request, user model.User, api http.Handler) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		if h.groupAPIAllowed(r, user) {
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
	ev, err := h.groupByAccount(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderGroupNotFound(w, r)
		} else {
			h.renderServiceError(w, r)
		}
		return
	}
	if ev.CustomWebsite && !ev.TakenDown {
		if _, err := h.opsSitesSvc().GetSiteByUser(r.Context(), ev.AccountID, ev.ID); err == nil {
			h.serveSiteFileRel(w, r, ev.AccountID, ev.ID, r.URL.Path)
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
			return
		}
	}
	if h.groupHosts != nil && h.groupHosts.ServePage(w, r, user) {
		return
	}
	h.renderGroupNotFound(w, r)
}

// Only the custom event website's own storage and visitor sign-in endpoints
// are served on its origin. Event management remains on the trusted apex.
func (h *SiteHandler) groupAPIAllowed(r *http.Request, owner model.User) bool {
	ev, err := h.groupByAccount(r.Context(), owner.ID)
	if err != nil || !ev.CustomWebsite || ev.TakenDown {
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

// renderGroupNotFound is the platform's 404 on an event or unknown host.
func (h *SiteHandler) renderGroupNotFound(w http.ResponseWriter, r *http.Request) {
	texts := h.groupTexts()
	h.renderNotFoundPage(w, r,
		texts.NotFoundTitle,
		texts.NotFoundBody,
		h.mainSiteURL(), "Go to "+h.siteDomain)
}

// noPersonalSites refuses a new site on the hackathon platform unless the
// caller is a team credential (or the admin); false after writing the answer.
func (h *SiteHandler) noPersonalSites(w http.ResponseWriter, user *model.User) bool {
	if h.ed.Identity().PersonalSites || user.IsAdmin || user.Team != nil || user.EventWebsite != nil {
		return true
	}
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: h.groupTexts().NoPersonalSites,
		Code:  "no_personal_sites",
	})
	return false
}

// groupPinnedFloor lowers a prune threshold so a team's deadline version is
// kept whatever the retention.
func (h *SiteHandler) groupPinnedFloor(ctx context.Context, userID, siteName string, keepFrom int) int {
	if !h.ed.Identity().TeamKeys || h.groupHosts == nil {
		return keepFrom
	}
	v, ok, err := h.groupHosts.PinnedVersion(ctx, userID, siteName)
	if err != nil {
		log.Printf("prune versions for %s: pinned version: %v", siteName, err)
		return 0 // keep everything rather than guess
	}
	if ok && v < keepFrom {
		return v
	}
	return keepFrom
}

// groupTLSHostExists bounds on-demand certificates to actual platform names.
// A real archived or taken-down host still needs HTTPS to show its page or
// notice; arbitrary names and deleted projects must not consume certificates.
// Used by the Caddy on-demand TLS gate (domains.go tlsAsk); a Kubernetes
// install with a static *.<base> wildcard never calls this.
func (h *SiteHandler) groupTLSHostExists(ctx context.Context, host string) bool {
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
				return h.groupSiteExists(ctx, event, team)
			}
		}
		labels := strings.Split(label, ".")
		if len(labels) < 1 || len(labels) > 2 {
			return false
		}
		if _, err := h.groupBySlug(ctx, labels[len(labels)-1]); err != nil {
			return false
		}
		if len(labels) == 1 {
			return true
		}
		return h.groupSiteExists(ctx, labels[1], labels[0])
	}
	return false
}

// groupSiteExists reports whether team is a real team of event with a
// live site: the on-demand TLS allowlist's team-site check, shared by the
// two-level (<team>.<event>.<base>) and flat (<team>--<event>.<base>) host
// shapes.
func (h *SiteHandler) groupSiteExists(ctx context.Context, eventSlug, teamSlug string) bool {
	ev, err := h.groupBySlug(ctx, eventSlug)
	if err != nil {
		return false
	}
	if ok, err := h.groupHosts.HasSite(ctx, ev.ID, teamSlug); err != nil || !ok {
		return false
	}
	site, err := h.sitesSvc().GetSiteByUser(ctx, ev.AccountID, teamSlug)
	return err == nil && site.ActiveVersion > 0
}
