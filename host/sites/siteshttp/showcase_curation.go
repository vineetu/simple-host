package siteshttp

import (
	"encoding/json"
	"errors"
	"net/http"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *SiteHandler) showcaseOwner(w http.ResponseWriter, r *http.Request) *model.User {
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

func (h *SiteHandler) showcaseBio(w http.ResponseWriter, r *http.Request) {
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
		bio, err := h.storeAccounts().SetShowcaseBio(r.Context(), u.ID, *body.Bio, max)
		writeShowcaseBio(w, bio, max, err)
		return
	}
	bio, err := h.storeAccounts().GetShowcaseBio(r.Context(), u.ID)
	writeShowcaseBio(w, bio, max, err)
}

func writeShowcaseBio(w http.ResponseWriter, bio string, max int, err error) {
	var refusal *accounts.Refusal
	switch {
	case errors.As(err, &refusal):
		httpx.WriteJSON(w, refusal.Status, httpx.ErrorResponse{Error: refusal.Message, Code: refusal.Code})
	case err != nil:
		httpx.WriteInternal(w)
	default:
		httpx.WriteJSON(w, 200, map[string]any{"bio": bio, "max_length": max})
	}
}

func (h *SiteHandler) showcaseSite(w http.ResponseWriter, r *http.Request) {
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
