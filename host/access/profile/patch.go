package profile

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	appsites "github.com/vsriram/simple-host/host/sites"
)

func normalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 100 {
		return "", errors.New("display_name must be at most 100 characters")
	}
	return name, nil
}

func (h *Patch) patchMe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, 401, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	var req profileRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if h.Ed.Identity().RandomHandles && req.Handle != nil && *req.Handle != user.Handle.String {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "Simple Hack accounts have no address to change", Code: "handle_fixed"})
		return
	}
	// A new address is judged first, so a taken, reserved or malformed one
	// is refused at once with the address named (JudgeHandle).
	if req.Handle != nil && *req.Handle != user.Handle.String {
		v, jerr := accounts.JudgeHandle(r.Context(), h.Database, user.ID, *req.Handle)
		if jerr != nil {
			httpx.WriteInternal(w)
			return
		}
		if v.Status != 0 {
			httpx.WriteJSON(w, v.Status, httpx.ErrorResponse{Error: v.Error, Code: v.Code, Address: v.Address})
			return
		}
	}
	if err := validateProfile(&req, user.Handle.String); err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if req.SignInAlerts != nil && user.KeyHash == "" {
		// A connected app (or a stolen connection) must not be able to
		// silence the email that would reveal it.
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "turning sign-in alerts on or off needs one of the account's own API keys (X-API-Key), not the admin key or a connected app", Code: "not_an_account_key"})
		return
	}
	updated, alertsOn, err := h.SiteFiles.UpdateProfile(r.Context(), appsites.ProfileInput{UserID: user.ID, DisplayName: req.DisplayName, Handle: req.Handle, SignInAlerts: req.SignInAlerts, RenameEvery: h.Limits.HandleRenameEvery})
	if err != nil {
		var failure *accounts.LifecycleError
		if errors.As(err, &failure) {
			if failure.Code == "rate_limited" {
				w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(failure.Next).Seconds())+1))
				httpx.WriteJSON(w, 429, map[string]any{"code": "rate_limited", "error": failure.Message, "next_change_after": failure.Next})
				return
			}
			if failure.Code == "handle_taken" {
				httpx.WriteJSON(w, 409, httpx.ErrorResponse{Error: accounts.HandleTakenMessage(*req.Handle), Code: "handle_taken", Address: accounts.HandleAddress(*req.Handle)})
				return
			}
		}
		auth.WriteLifecycleFailure(w, err)
		return
	}

	httpx.WriteJSON(w, 200, meResponse{ID: updated.ID, Username: updated.Username, IsAdmin: updated.IsAdmin, Handle: updated.Handle.String, DisplayName: updated.DisplayName.String, PublicPage: h.Hosts.Addressing().PersonPageURL(updated.Handle.String), SignInAlerts: &alertsOn})
}

type profileRequest struct {
	DisplayName  *string `json:"display_name"`
	Handle       *string `json:"handle"`
	SignInAlerts *bool   `json:"signin_alerts"`
}

// validateProfile checks the fields sent; a handle only when it changes
// (a client may echo the current one, which may predate today's rules).
func validateProfile(req *profileRequest, current string) error {
	if req.DisplayName == nil && req.Handle == nil && req.SignInAlerts == nil {
		return errors.New("provide display_name, handle and/or signin_alerts")
	}
	if req.DisplayName != nil {
		name, err := normalizeDisplayName(*req.DisplayName)
		if err != nil {
			return err
		}
		*req.DisplayName = name
	}
	if req.Handle != nil && *req.Handle != current {
		return accounts.CheckNewHandle(*req.Handle)
	}
	return nil
}

// Patch serves PATCH /v1/me: the display name, handle and sign-in alerts.
// SiteFiles renames the account's sites with its handle.
type Patch struct {
	Database  *sql.DB
	Hosts     *sites.Hosts
	SiteFiles *sites.Service
	Limits    edition.Limits
	Ed        *edition.Edition
}

// Register mounts PATCH /v1/me.
func (h *Patch) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("PATCH /v1/me", authMiddleware(http.HandlerFunc(h.patchMe)))
}
