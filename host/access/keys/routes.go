package keys

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// Handler serves a person's API keys and sign-out.
type Handler struct {
	DB     *sql.DB
	Limits edition.Limits
}

// Register mounts the key routes.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/me/api-key/rotate", noticeMiddleware(authMiddleware(http.HandlerFunc(h.RotateAPIKey))))
	mux.Handle("POST /v1/me/sign-out", noticeMiddleware(authMiddleware(http.HandlerFunc(h.signOut))))
	mux.Handle("GET /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listKeys))))
	mux.Handle("POST /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createKey))))
	mux.Handle("DELETE /v1/me/keys/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteKey))))
}

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

// signOut handles POST /v1/me/sign-out: the key the request came with stops
// working. The browser clears its copy afterwards.
func (h *Handler) signOut(w http.ResponseWriter, r *http.Request) {
	user := auth.AccountKeyUser(w, r, "signing out")
	if user == nil {
		return
	}
	browserPass := auth.SignOutBrowserPass(r)
	if err := SignOut(r.Context(), h.DB, user.ID, user.KeyHash, browserPass); err != nil {
		log.Printf("sign out user_id=%s: %v", user.ID, err)
		httpx.WriteInternal(w)
		return
	}

	auth.SetSSOCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "missing_api_key", "unauthorized")
		return
	}
	keys, err := ListKeys(r.Context(), h.DB, user.ID, h.Limits.KeyIdleExpiry)
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
func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	user := auth.AccountKeyUser(w, r, "creating a key")
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
	k, err := IssueKey(r.Context(), h.DB, user, req, plain, h.Limits.MaxKeysPerAccount, h.Limits.KeyIdleExpiry)
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
func (h *Handler) writeCreateKeyError(w http.ResponseWriter, userID string, err error) {
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

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	user := auth.AccountKeyUser(w, r, "revoking a key")
	if user == nil {
		return
	}
	ok, err := DeleteKey(r.Context(), h.DB, user.ID, r.PathValue("id"))
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

func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if user.KeyHash == "" {
		// The env admin key, or a connected app acting for the person.
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "only an account API key can be rotated through this endpoint"})
		return
	}
	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	err = RotateKey(r.Context(), h.DB, user.ID, user.KeyHash, newKey)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: auth.InvalidKeyMessage(), Code: "invalid_api_key"})
		return
	}
	if errors.Is(err, ErrDisconnectAfterRotation) {
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"api_key": newKey,
		"message": "API key rotated. The old key no longer works; update your agent or CLI with this new key. Connected apps (ChatGPT, Claude, Grok) were disconnected and sign-ins on your sites ended; reconnect the ones you use.",
	})
}
