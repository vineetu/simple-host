package showcase

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// routes are the showcase and home handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the showcase feed, the bio, a site's showcase pin and order,
// and the home page setting.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	s := routes{h}
	mux.Handle("GET /v1/u/{handle}/showcase.json", http.HandlerFunc(s.showcaseFeed))
	mux.Handle("GET /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(s.showcaseBio))))
	mux.Handle("PUT /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(s.showcaseBio))))
	mux.Handle("GET /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(s.showcaseSite))))
	mux.Handle("PUT /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(s.showcaseSite))))
	mux.Handle("GET /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(s.homeSetting))))
	mux.Handle("PUT /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(s.homeSetting))))
}

func (h routes) showcaseOwner(w http.ResponseWriter, r *http.Request) *model.User {
	w.Header().Set("Cache-Control", "no-store")
	if h.Ed.Routes().HostedEvents {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "showcase settings are unavailable", Code: "not_found"})
		return nil
	}
	u := auth.GetUser(r.Context())
	if u == nil {
		httpx.WriteJSON(w, 401, httpx.ErrorResponse{Error: "unauthorized"})
		return nil
	}
	if u.DeployOnly() {
		httpx.WriteJSON(w, 403, httpx.ErrorResponse{Error: auth.DeployOnlyMessage, Code: "deploy_only_key"})
		return nil
	}
	return u
}

func (h routes) showcaseBio(w http.ResponseWriter, r *http.Request) {
	u := h.showcaseOwner(w, r)
	if u == nil {
		return
	}
	max := h.Limits.ShowcaseBioMaxLength
	if r.Method == http.MethodPut {
		var body struct {
			Bio *string `json:"bio"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || body.Bio == nil {
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "send a plain-text bio"})
			return
		}
		bio, err := h.StoreAccounts().SetShowcaseBio(r.Context(), u.ID, *body.Bio, max)
		writeShowcaseBio(w, bio, max, err)
		return
	}
	bio, err := h.StoreAccounts().GetShowcaseBio(r.Context(), u.ID)
	writeShowcaseBio(w, bio, max, err)
}

func writeShowcaseBio(w http.ResponseWriter, bio string, max int, err error) {
	var refusal *accountapp.Refusal
	switch {
	case errors.As(err, &refusal):
		httpx.WriteJSON(w, refusal.Status, httpx.ErrorResponse{Error: refusal.Message, Code: refusal.Code})
	case err != nil:
		httpx.WriteInternal(w)
	default:
		httpx.WriteJSON(w, 200, map[string]any{"bio": bio, "max_length": max})
	}
}

func (h routes) showcaseSite(w http.ResponseWriter, r *http.Request) {
	u := h.showcaseOwner(w, r)
	if u == nil {
		return
	}
	name := r.PathValue("sitename")
	var pref model.ShowcasePreference
	var err error
	if r.Method == http.MethodPut {
		var body struct {
			Pinned *bool `json:"pinned"`
			Order  *int  `json:"order"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || (body.Pinned == nil && body.Order == nil) || (body.Order != nil && (*body.Order < 0 || *body.Order > 1000000)) {
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "send pinned (boolean) and/or order (integer from 0 to 1000000)"})
			return
		}
		pref, err = h.SitesSvc().SetShowcasePreference(r.Context(), u.ID, name, body.Pinned, body.Order)
	} else {
		pref, err = h.SitesSvc().GetShowcasePreference(r.Context(), u.ID, name)
	}
	var refusal *sites.Error
	switch {
	case errors.As(err, &refusal):
		httpx.WriteJSON(w, httpx.RefusalStatus(refusal.Code, http.StatusInternalServerError), httpx.ErrorResponse{Error: refusal.Message, Code: refusal.Code})
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"site": name, "pinned": pref.Pinned, "order": pref.Order})
}

func (h routes) showcaseFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
	w.Header().Set("Cache-Control", "no-store")
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if h.Ed.Routes().HostedEvents || !sites.ShowcaseHandle(handle) {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "not found", Code: "not_found"})
		return
	}
	user, err := h.StoreAccounts().GetUserByHandle(r.Context(), handle)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if h.ShowcaseLimiter != nil && !h.ShowcaseLimiter.Allow("showcase:"+user.ID+":"+httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return
	}
	data, err := h.PublicShowcaseData(r.Context(), user)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteJSON(w, 200, map[string]any{"handle": data.Handle, "bio": data.Bio, "sites": data.Sites})
}

func (h routes) homeSetting(w http.ResponseWriter, r *http.Request) {
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
	var refusal *sites.Error
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
