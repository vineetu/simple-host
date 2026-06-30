package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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

// Middleware authenticates X-API-Key. adminUserID is the real UUID of the
// seeded `admin` users row, so the admin identity can own sites (the old
// synthetic ID:"admin" violated the sites.user_id UUID foreign key).
func Middleware(adminAPIKey, adminUserID string, database *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				// Most common mistake (humans and LLMs alike): sending the key as
				// `Authorization: Bearer <key>`. This API only reads X-API-Key, so
				// point them at the right header instead of a bare "unauthorized".
				msg := "missing API key: send it as the header 'X-API-Key: <key>'. " +
					"Get a key via POST /v1/auth then POST /v1/auth/verify. See /llms.txt."
				if r.Header.Get("Authorization") != "" {
					msg = "missing X-API-Key header: this API authenticates with " +
						"'X-API-Key: <key>', not 'Authorization: Bearer'. Resend your key " +
						"in the X-API-Key header. See /llms.txt."
				}
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msg})
				return
			}

			// Check hardcoded admin key first. Constant-time compare so the
			// match can't be inferred from response timing.
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(adminAPIKey)) == 1 {
				adminUser := &db.User{
					ID:       adminUserID,
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
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key: the X-API-Key you sent is not recognized. If it expired or leaked, sign in again via POST /v1/auth."})
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
