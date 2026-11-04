package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *SiteHandler) showcaseOwner(w http.ResponseWriter, r *http.Request) *model.User {
	w.Header().Set("Cache-Control", "no-store")
	if h.ed.Routes().HostedEvents {
		writeJSON(w, 404, errorResponse{Error: "showcase settings are unavailable", Code: "not_found"})
		return nil
	}
	u := auth.GetUser(r.Context())
	if u == nil {
		writeJSON(w, 401, errorResponse{Error: "unauthorized"})
		return nil
	}
	if u.DeployOnly() {
		writeJSON(w, 403, errorResponse{Error: auth.DeployOnlyMessage, Code: "deploy_only_key"})
		return nil
	}
	return u
}

func normalizeBio(bio string, max int) (string, error) {
	bio = strings.TrimSpace(bio)
	if utf8.RuneCountInString(bio) > max {
		return "", fmt.Errorf("bio must be at most %d characters", max)
	}
	return bio, nil
}

func (h *SiteHandler) showcaseBio(w http.ResponseWriter, r *http.Request) {
	u := h.showcaseOwner(w, r)
	if u == nil {
		return
	}
	max := h.limits.ShowcaseBioMaxLength
	if r.Method == http.MethodPut {
		var body struct {
			Bio *string `json:"bio"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || body.Bio == nil {
			writeJSON(w, 400, errorResponse{Error: "send a plain-text bio"})
			return
		}
		bio, err := normalizeBio(*body.Bio, max)
		if err != nil {
			writeJSON(w, 400, errorResponse{Error: err.Error(), Code: "bio_too_long"})
			return
		}
		if err := h.storeAccounts().SetShowcaseBio(r.Context(), u.ID, bio); err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
	}
	bio, err := h.storeAccounts().GetShowcaseBio(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, 200, map[string]any{"bio": bio, "max_length": max})
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
			writeJSON(w, 400, errorResponse{Error: "send pinned (boolean) and/or order (integer from 0 to 1000000)"})
			return
		}
		pref, err = h.sitesSvc().SetShowcasePreference(r.Context(), u.ID, name, body.Pinned, body.Order)
	} else {
		pref, err = h.sitesSvc().GetShowcasePreference(r.Context(), u.ID, name)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, errorResponse{Error: "site not found", Code: "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, 200, map[string]any{"site": name, "pinned": pref.Pinned, "order": pref.Order})
}
