package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Account keys one at a time: list them, mint a named one, revoke one, and
// sign out (revoke the key the request came with). Rotate (user.go) stays the
// "revoke everything" lever.

const maxKeyNameLen = 60

// normalizeKeyName trims a typed key name; "" means "use the default".
func normalizeKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxKeyNameLen {
		return "", errors.New("name must be at most 60 characters")
	}
	// Cf covers zero-width and bidi overrides (U+200B, U+202E), which make a
	// name read differently from what it is.
	if strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return "", errors.New("name must not contain control or invisible formatting characters")
	}
	return name, nil
}

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

func newKeyView(k db.APIKey, now time.Time, current bool) keyView {
	return keyView{
		ID: k.ID, Name: k.Name, Last4: k.Last4, Scope: k.Scope, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		ExpiresAt: k.ExpiresAt, IdleExpiresAt: k.IdleExpiresAt(), Expired: k.Expired(now), Current: current,
	}
}

// maxKeyExpiryDays bounds expires_in_days at mint (about ten years).
const maxKeyExpiryDays = 3650

// accountKeyUser returns the signed-in person, and refuses (400
// not_an_account_key) a request that did not come with one of the account's
// stored keys: the env admin key, or a connected app acting for the person.
// A connected app must not turn its grant into a key that outlives it.
func accountKeyUser(w http.ResponseWriter, r *http.Request, what string) *db.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "code": "missing_api_key"})
		return nil
	}
	if user.KeyHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
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
	if _, err := db.DeleteAPIKeyByHash(r.Context(), h.database, user.ID, user.KeyHash); err != nil {
		log.Printf("sign out user_id=%s: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "code": "missing_api_key"})
		return
	}
	keys, err := db.ListAPIKeys(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]keyView, 0, len(keys))
	now := time.Now()
	for _, k := range keys {
		out = append(out, newKeyView(k, now, user.KeyHash != "" && k.Hash == user.KeyHash))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// createKey handles POST /v1/me/keys {name, scope, expires_in_days}: a named
// key for a CI secret or another machine, shown once. scope "deploy" makes a
// deploy-only key (internal/auth/scope.go); expires_in_days an optional fixed
// expiry.
func (h *UserHandler) createKey(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "creating a key")
	if user == nil {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	name, err := normalizeKeyName(req.Name)
	if err == nil && name == "" {
		err = errors.New("name is required: say where the key will live, e.g. \"GitHub Actions\"")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	scope := strings.TrimSpace(req.Scope)
	switch scope {
	case "":
		scope = db.KeyScopeFull
	case db.KeyScopeFull, db.KeyScopeDeploy:
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `scope must be "full" (the default) or "deploy" (create, update, roll back and list sites, and preview links only)`})
		return
	}
	var expiresAt *time.Time
	if req.ExpiresInDays != nil {
		n := *req.ExpiresInDays
		if n < 1 || n > maxKeyExpiryDays {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("expires_in_days must be 1..%d, or left out for a key that expires only when unused", maxKeyExpiryDays)})
			return
		}
		t := time.Now().Add(time.Duration(n) * 24 * time.Hour).UTC()
		expiresAt = &t
	}
	plain, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	k, err := db.CreateAPIKey(r.Context(), h.database, user.ID, user.KeyHash, plain, name, scope, expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		// The key this request came with was revoked or rotated away meanwhile.
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key: the X-API-Key you sent is not recognized (it may have been revoked, or the account signed out). Sign in again via POST /v1/auth for a new key.", Code: "invalid_api_key"})
		return
	}
	if errors.Is(err, db.ErrKeyLimit) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("this account already holds %d keys; revoke ones you no longer use first", db.MaxAccountKeys()), Code: "key_limit"})
		return
	}
	if err != nil {
		log.Printf("create key user_id=%s: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	v := newKeyView(k, time.Now(), false)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": v.ID, "name": v.Name, "last4": v.Last4, "scope": v.Scope, "created_at": v.CreatedAt, "last_used_at": nil,
		"expires_at": v.ExpiresAt, "idle_expires_at": v.IdleExpiresAt,
		"api_key": plain,
		"message": "Copy this key now; it is not shown again. Revoke it any time from the Keys list.",
	})
}

func (h *UserHandler) deleteKey(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "revoking a key")
	if user == nil {
		return
	}
	ok, err := db.DeleteAPIKey(r.Context(), h.database, user.ID, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no key with that id on this account"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
