package handler

import (
	"encoding/json"
	"errors"
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
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("name must not contain control characters")
	}
	return name, nil
}

type keyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	Last4      string     `json:"last4,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Current    bool       `json:"current"`
}

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
	for _, k := range keys {
		out = append(out, keyView{
			ID: k.ID, Name: k.Name, Last4: k.Last4, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
			Current: user.KeyHash != "" && k.Hash == user.KeyHash,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// createKey handles POST /v1/me/keys {name}: a named key for a CI secret or
// another machine, shown once.
func (h *UserHandler) createKey(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "creating a key")
	if user == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
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
	plain, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	k, err := db.CreateAPIKey(r.Context(), h.database, user.ID, plain, name)
	if err != nil {
		log.Printf("create key user_id=%s: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": k.ID, "name": k.Name, "last4": k.Last4, "created_at": k.CreatedAt, "last_used_at": nil,
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
