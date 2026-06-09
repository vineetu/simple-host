package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/db"
)

type contextKey int

const userContextKey contextKey = iota

type errorResponse struct {
	Error string `json:"error"`
}

func GenerateAPIKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}

	return hex.EncodeToString(key), nil
}

func Middleware(adminAPIKey string, database *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
				return
			}

			// Check hardcoded admin key first
			if apiKey == adminAPIKey {
				adminUser := &db.User{
					ID:       "admin",
					Username: "admin",
					IsAdmin:  true,
				}
				ctx := context.WithValue(r.Context(), userContextKey, adminUser)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			user, err := db.GetUserByAPIKey(r.Context(), database, apiKey)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
					return
				}

				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		if user == nil || !user.IsAdmin {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func GetUser(ctx context.Context) *db.User {
	user, _ := ctx.Value(userContextKey).(*db.User)
	return user
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
