package siteshttp

import (
	"net/http"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/pagepath"
)

func (h *SiteHandler) homePathExists(s model.Site, rel string) bool {
	root, err := os.OpenRoot(h.Disk.SiteDir(s.UserID, s.Name) + "/current")
	if err != nil {
		return false
	}
	defer root.Close()
	return pagepath.Resolve(root, rel).Kind != pagepath.NotFound
}

func (h *SiteHandler) serveHomePage(w http.ResponseWriter, r *http.Request, user model.User, base string) bool {
	s, has, err := h.SitesSvc().HomeSite(r.Context(), user.ID)
	if err != nil {
		h.renderServiceError(w, r)
		return true
	}
	if !has || !s.Live() {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	redirectSibling := func() bool {
		seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if other, err := h.SitesSvc().AddressedSite(r.Context(), user.ID, seg); err == nil {
			_, escTail, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
			if other.ID == s.ID {
				target := "/" + strings.TrimLeft(escTail, "/")
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusFound)
				return true
			}
			if !h.Hosts.Addressing().SiteHostCanonicalOn(user.Handle.String, other.Name, base) {
				if _, has, err := h.Hosts.SiteOwnAddress(r.Context(), other.ID); err == nil && !has {
					// Serving another site's old path here would give it the home's origin.
					// Never redirect to an unissued TLS name or back to this same path.
					h.renderServiceError(w, r)
					return true
				}
			}
			target := h.Hosts.Addressing().SiteAddressWithPathOn(user.Handle.String, other.Name, "/"+strings.TrimLeft(escTail, "/"), base)
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusFound)
			return true
		}
		return false
	}
	probe := func() bool {
		return !h.homePathExists(s, r.URL.Path) && redirectSibling()
	}
	h.serveSiteFileWithProbe(w, r, s.UserID, s.Name, r.URL.Path, r.URL.EscapedPath(), probe)
	return true
}
