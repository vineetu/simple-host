package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

func (h *SiteHandler) groupTexts() web.GroupTexts {
	if g := h.Hosts.Groups(); g != nil {
		return g.Texts()
	}
	if texts := h.Pages().Product.Hooks.GroupTexts; texts != nil {
		return texts()
	}
	return web.GroupTexts{NotFoundTitle: "Page not found", NotFoundBody: "No page lives at this address."}
}

// ServeGroupHost answers <event>.<SITE_DOMAIN> in hosted mode. A custom
// event site stays on this origin; the signed-in app and every API stay apex-only.
func (h *SiteHandler) ServeGroupHost(w http.ResponseWriter, r *http.Request, user model.User, api http.Handler) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		if h.GroupAPIAllowed(r, user) {
			api.ServeHTTP(w, r)
			return
		}
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ev, err := h.Hosts.GroupByAccount(r.Context(), user.ID)
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
			h.ServeSiteFileRel(w, r, ev.AccountID, ev.ID, r.URL.Path)
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
			return
		}
	}
	if h.Hosts.Groups() != nil && h.Hosts.Groups().ServePage(w, r, user) {
		return
	}
	h.renderGroupNotFound(w, r)
}

// Only the custom event website's own storage and visitor sign-in endpoints
// are served on its origin. Event management remains on the trusted apex.
func (h *SiteHandler) GroupAPIAllowed(r *http.Request, owner model.User) bool {
	ev, err := h.Hosts.GroupByAccount(r.Context(), owner.ID)
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

func (h *SiteHandler) renderGroupNotFound(w http.ResponseWriter, r *http.Request) {
	texts := h.groupTexts()
	h.renderNotFoundPage(w, r,
		texts.NotFoundTitle,
		texts.NotFoundBody,
		h.mainSiteURL(), "Go to "+h.SiteDomain)
}

// groupPinnedFloor lowers a prune threshold so a team's deadline version is
// kept whatever the retention.
func (h *SiteHandler) groupPinnedFloor(ctx context.Context, userID, siteName string, keepFrom int) int {
	if h.Hosts.Groups() == nil {
		return keepFrom
	}
	v, ok, err := h.Hosts.Groups().PinnedVersion(ctx, userID, siteName)
	if err != nil {
		log.Printf("prune versions for %s: pinned version: %v", siteName, err)
		return 0 // keep everything rather than guess
	}
	if ok && v < keepFrom {
		return v
	}
	return keepFrom
}
