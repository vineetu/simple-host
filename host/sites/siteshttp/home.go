package siteshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/pagepath"
)

func (h *SiteHandler) homeSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Ed.Routes().HostedEvents {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "home pages are unavailable", Code: "not_found"})
		return
	}
	u := auth.GetUser(r.Context())
	if u == nil {
		httpx.WriteJSON(w, 401, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if u.DeployOnly() {
		httpx.WriteJSON(w, 403, httpx.ErrorResponse{Error: auth.DeployOnlyMessage, Code: "deploy_only_key"})
		return
	}
	if r.Method == http.MethodPut {
		var body map[string]json.RawMessage
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if dec.Decode(&body) != nil || len(body) != 1 || body["site"] == nil {
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "send site as a name or null"})
			return
		}
		var name *string
		if json.Unmarshal(body["site"], &name) != nil {
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "send site as a name or null"})
			return
		}
		s, has, err := h.SitesSvc().SetHomeSite(r.Context(), u, name)
		writeHomeSite(w, s, has, err)
		return
	}
	s, has, err := h.SitesSvc().HomeSite(r.Context(), u.ID)
	writeHomeSite(w, s, has, err)
}

// writeHomeSite answers GET and PUT /v1/me/home.
func writeHomeSite(w http.ResponseWriter, s model.Site, has bool, err error) {
	var refusal *appsites.Error
	switch {
	case errors.As(err, &refusal):
		httpx.WriteJSON(w, httpx.RefusalStatus(refusal.Code, http.StatusConflict), httpx.ErrorResponse{Error: refusal.Message, Code: refusal.Code})
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	var name any
	if has {
		name = s.Name
	}
	httpx.WriteJSON(w, 200, map[string]any{"site": name})
}

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
