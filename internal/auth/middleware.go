package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

type contextKey int

const userContextKey contextKey = iota

// errorResponse carries a machine-readable code on every 401 so a script can
// branch on it: missing_api_key, wrong_auth_header or invalid_api_key.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// APIKeyPrefix starts every newly issued account key, so secret scanners (and
// people) recognise one in a log, a commit or a paste. Lookup is by the hash of
// whatever is sent, so keys issued before the prefix (bare hex) keep working.
const APIKeyPrefix = "shk_"

// SupportContact is who a person writes to about their account: the
// hosted service's support address, or on another install whoever runs it
// (SetSupportContact, from main).
var SupportContact = "support@simple-host.app"

// keyHelp is how a person gets a new key: signing in again with an emailed
// code, or on an install that sends no email, from whoever runs it.
var keyHelp = "Sign in again via POST /v1/auth for a new key."

// SetSupportContact sets SupportContact and, when the install sends no email
// (so nobody can sign in with a code), the key help. Call once at startup.
func SetSupportContact(contact string, emailSignIn bool) {
	if contact != "" {
		SupportContact = contact
	}
	keyHelp = "Sign in again via POST /v1/auth for a new key."
	if !emailSignIn {
		keyHelp = "Ask whoever runs this server for a new key (its admin page issues one)."
	}
}

// InvalidKeyMessage is the 401 invalid_api_key text.
func InvalidKeyMessage() string {
	return "invalid API key: the X-API-Key you sent is not recognized (it may have been revoked, or the account signed out). " + keyHelp
}

func GenerateAPIKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}

	return APIKeyPrefix + hex.EncodeToString(key), nil
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
				code := "missing_api_key"
				if r.Header.Get("Authorization") != "" {
					msg = "missing X-API-Key header: this API authenticates with " +
						"'X-API-Key: <key>', not 'Authorization: Bearer'. Resend your key " +
						"in the X-API-Key header. See /llms.txt."
					code = "wrong_auth_header"
				}
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msg, Code: code})
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
				if errors.Is(err, db.ErrAccountSuspended) {
					// The key is kept, not deleted, so re-enabling the account
					// brings it back; the person sees why here.
					msg := SuspendedMessage(user.SuspendedReason)
					if r.URL.Path == "/v1/me/export.zip" || r.URL.Path == "/v1/me/export.tar.gz" {
						// The right of access stands while suspended.
						msg += "; write to " + SupportContact + " for a copy of your data"
					}
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error":  msg,
						"code":   "account_suspended",
						"reason": user.SuspendedReason,
					})
					return
				}
				if errors.Is(err, db.ErrKeyExpired) || errors.Is(err, db.ErrKeyExpiredIdle) {
					msg, code := ExpiredKeyMessage(err, user.KeyExpiresAt)
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msg, Code: code})
					return
				}
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: InvalidKeyMessage(), Code: "invalid_api_key"})
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

// ExpiredKeyMessage is the 401 a stored key that stopped working gets, and its
// code: key_expired (past the expiry chosen at mint) or key_expired_idle
// (unused for KEY_IDLE_EXPIRY_DAYS).
func ExpiredKeyMessage(err error, expiredAt *time.Time) (string, string) {
	if errors.Is(err, db.ErrKeyExpired) {
		msg := "this API key has expired"
		if expiredAt != nil {
			msg += " (on " + expiredAt.UTC().Format("2 January 2006") + ")"
		}
		return msg + ". Create a new key from the Keys panel on your Simple Host page, or sign in again via POST /v1/auth.", "key_expired"
	}
	days := int(db.KeyIdleExpiry().Hours() / 24)
	return fmt.Sprintf("this API key stopped working because it was not used for %d days. "+
		"Create a new key from the Keys panel on your Simple Host page, or sign in again via POST /v1/auth.", days), "key_expired_idle"
}

// SuspendedMessage is the error text a suspended person sees, with the
// operator's reason when there is one.
func SuspendedMessage(reason string) string {
	if reason == "" {
		return "this account has been suspended by the operator"
	}
	return "this account has been suspended by the operator: " + reason
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

// WithUser returns ctx acting as user. Only for a caller that has already
// authorised the switch (the admin's per-site actions run the owner routes as
// the site's owner, handler/adminsite.go).
func WithUser(ctx context.Context, user *db.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
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
