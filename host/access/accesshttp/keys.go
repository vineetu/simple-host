package accesshttp

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// Account keys one at a time: list them, mint a named one, revoke one, and
// sign out (revoke the key the request came with). Rotate (user.go) stays the
// "revoke everything" lever.

type keyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	Last4      string     `json:"last4,omitempty"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// ExpiresAt is the fixed expiry chosen at mint (null: none).
	ExpiresAt *time.Time `json:"expires_at"`
	// IdleExpiresAt is when the key stops working unless used before then
	// (null when KEY_IDLE_EXPIRY_DAYS=0).
	IdleExpiresAt *time.Time `json:"idle_expires_at"`
	// Expired is "expired" or "idle" once the key has stopped working.
	Expired string `json:"expired,omitempty"`
	Current bool   `json:"current"`
}

func newKeyView(k accounts.KeyInfo, current bool) keyView {
	return keyView{
		ID: k.ID, Name: k.Name, Last4: k.Last4, Scope: k.Scope, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		ExpiresAt: k.ExpiresAt, IdleExpiresAt: k.IdleExpiresAt, Expired: k.Expired, Current: current,
	}
}

// accountKeyUser returns the signed-in person, and refuses (400
// not_an_account_key) a request that did not come with one of the account's
// stored keys: the env admin key, or a connected app acting for the person.
// A connected app must not turn its grant into a key that outlives it.
func accountKeyUser(w http.ResponseWriter, r *http.Request, what string) *model.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "missing_api_key", "unauthorized")
		return nil
	}
	if user.KeyHash == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": what + " needs one of the account's own API keys (X-API-Key), not the admin key or a connected app",
			"code":  "not_an_account_key",
		})
		return nil
	}
	return user
}

// signOut handles POST /v1/me/sign-out: the key the request came with stops
// working. The browser clears its copy afterwards.
func (h *UserHandler) signOut(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "signing out")
	if user == nil {
		return
	}
	browserPass := signOutBrowserPass(r)
	if err := h.accounts.SignOut(r.Context(), user.ID, user.KeyHash, browserPass); err != nil {
		log.Printf("sign out user_id=%s: %v", user.ID, err)
		httpx.WriteInternal(w)
		return
	}

	setSSOCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "missing_api_key", "unauthorized")
		return
	}
	keys, err := h.accounts.ListKeys(r.Context(), user.ID, h.Limits.KeyIdleExpiry)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]keyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, newKeyView(k, user.KeyHash != "" && k.Hash == user.KeyHash))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// createKey handles POST /v1/me/keys {name, scope, expires_in_days}: a named
// key for a CI secret or another machine, shown once. scope "deploy" makes a
// deploy-only key (host/auth/scope.go); expires_in_days an optional fixed
// expiry.
func (h *UserHandler) createKey(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "creating a key")
	if user == nil {
		return
	}
	var req accounts.NewKeyRequest
	if !httpx.DecodeJSON(w, r, 4096, &req) {
		return
	}
	plain, err := auth.GenerateAPIKey()
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	k, err := h.accounts.IssueKey(r.Context(), user, req, plain, h.Limits.MaxKeysPerAccount, h.Limits.KeyIdleExpiry)
	if err != nil {
		h.writeCreateKeyError(w, user.ID, err)
		return
	}
	v := newKeyView(k, false)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": v.ID, "name": v.Name, "last4": v.Last4, "scope": v.Scope, "created_at": v.CreatedAt, "last_used_at": nil,
		"expires_at": v.ExpiresAt, "idle_expires_at": v.IdleExpiresAt,
		"api_key": plain,
		"message": "Copy this key now; it is not shown again. Revoke it any time from the Keys list.",
	})
}

// writeCreateKeyError answers a key that could not be made: the request is
// malformed, the key this
// request came with was revoked or rotated away meanwhile, or the account
// holds its limit.
func (h *UserHandler) writeCreateKeyError(w http.ResponseWriter, userID string, err error) {
	var refusal *accounts.Refusal
	switch {
	case errors.As(err, &refusal):
		web.WriteAccessError(w, err)
	case errors.Is(err, accounts.ErrCallerKeyGone):
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: auth.InvalidKeyMessage(), Code: "invalid_api_key"})
	case errors.Is(err, accounts.ErrKeyLimit):
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: fmt.Sprintf("this account already holds %d keys; revoke ones you no longer use first", h.Limits.MaxKeysPerAccount), Code: "key_limit"})
	default:
		log.Printf("create key user_id=%s: %v", userID, err)
		httpx.WriteInternal(w)
	}
}

func (h *UserHandler) deleteKey(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "revoking a key")
	if user == nil {
		return
	}
	ok, err := h.accounts.DeleteKey(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "no key with that id on this account"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
