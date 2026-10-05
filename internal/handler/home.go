package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) homeSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if hackMode {
		writeJSON(w, 404, errorResponse{Error: "home pages are unavailable", Code: "not_found"})
		return
	}
	u := auth.GetUser(r.Context())
	if u == nil {
		writeJSON(w, 401, errorResponse{Error: "unauthorized"})
		return
	}
	if u.KeyScope == db.KeyScopeDeploy {
		writeJSON(w, 403, errorResponse{Error: auth.DeployOnlyMessage, Code: "deploy_only_key"})
		return
	}
	if r.Method == http.MethodPut {
		var body map[string]json.RawMessage
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		dec := json.NewDecoder(r.Body)
		if dec.Decode(&body) != nil || len(body) != 1 || body["site"] == nil {
			writeJSON(w, 400, errorResponse{Error: "send site as a name or null"})
			return
		}
		var name *string
		if json.Unmarshal(body["site"], &name) != nil || (name != nil && !validSiteName.MatchString(*name)) {
			writeJSON(w, 400, errorResponse{Error: "send site as a name or null"})
			return
		}
		if name != nil && contentHostOnlySites[u.Handle.String+"/"+*name] {
			writeJSON(w, 409, errorResponse{Error: "this site depends on the content host"})
			return
		}
		if err := db.SetHomeSite(r.Context(), h.database, u.ID, name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, 404, errorResponse{Error: "site not found", Code: "not_found"})
			} else {
				writeJSON(w, 500, errorResponse{Error: "internal server error"})
			}
			return
		}
	}
	s, has, err := db.HomeSite(r.Context(), h.database, u.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	var name any
	if has {
		name = s.Name
	}
	writeJSON(w, 200, map[string]any{"site": name})
}

// Selected home is an additional origin of exactly one site, even when it has a domain.
func (h *SiteHandler) isHomeSiteHost(ctx context.Context, siteID, host string) bool {
	if hackMode {
		return false
	}
	owner, ok := h.personHostOwner(ctx, host)
	if !ok {
		return false
	}
	s, has, err := db.HomeSite(ctx, h.database, owner.ID)
	return err == nil && has && s.ID == siteID && !s.Deleted && !s.Offline && !s.Suspended()
}

// homePathExists mirrors file/directory lookup without escaping the site's root.
func (h *SiteHandler) homePathExists(s db.Site, rel string) bool {
	root, err := os.OpenRoot(h.disk.SiteDir(s.UserID, s.Name) + "/current")
	if err != nil {
		return false
	}
	defer root.Close()
	name := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if name == "" {
		name = "."
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	if !st.IsDir() {
		return true
	}
	ix, err := root.Open(path.Join(name, "index.html"))
	if err != nil {
		return false
	}
	defer ix.Close()
	st, err = ix.Stat()
	return err == nil && !st.IsDir()
}

func (h *SiteHandler) serveHomePage(w http.ResponseWriter, r *http.Request, user db.User, base string) bool {
	s, has, err := db.HomeSite(r.Context(), h.database, user.ID)
	if err != nil {
		h.renderServiceError(w, r)
		return true
	}
	if !has || s.Offline || s.Suspended() {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	// Gate before checking for a collision: a locked site's files stay undiscoverable.
	if h.siteGate(w, r, s.UserID, s.Name, r.URL.Path) {
		return true
	}
	if !h.homePathExists(s, r.URL.Path) {
		seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if validSiteName.MatchString(seg) {
			if other, err := db.GetSiteByUser(r.Context(), h.database, user.ID, seg); err == nil {
				_, escTail, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
				if other.ID == s.ID {
					target := "/" + strings.TrimLeft(escTail, "/")
					if r.URL.RawQuery != "" {
						target += "?" + r.URL.RawQuery
					}
					http.Redirect(w, r, target, http.StatusFound)
					return true
				}
				if !h.siteHostCanonicalOn(user.Handle.String, other.Name, base) {
					if _, has, err := h.siteOwnAddress(r.Context(), other.ID); err == nil && !has {
						// Serving another site's old path here would give it the home's origin.
						// Never redirect to an unissued TLS name or back to this same path.
						h.renderServiceError(w, r)
						return true
					}
				}
				target := h.siteAddressWithPathOn(user.Handle.String, other.Name, "/"+strings.TrimLeft(escTail, "/"), base)
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusFound)
				return true
			}
		}
	}
	h.serveSiteFile(w, r, s.UserID, s.Name, r.URL.Path, r.URL.EscapedPath())
	return true
}
