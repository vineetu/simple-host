package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

type contextKey int

const userContextKey contextKey = iota

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
// synthetic ID:"admin" violated the sites.user_id UUID foreign key). keyIdle
// is the instance's KEY_IDLE_EXPIRY_DAYS limit (edition.Edition.Limits()).
func Middleware(adminAPIKey, adminUserID string, accountService *accounts.Service, keyIdle time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				writeMissingKey(w, r)
				return
			}
			// Check hardcoded admin key first. Constant-time compare so the
			// match can't be inferred from response timing.
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(adminAPIKey)) == 1 {
				adminUser := &model.User{ID: adminUserID, Username: "admin", IsAdmin: true}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, adminUser)))
				return
			}
			user, err := accountService.GetUserByAPIKey(r.Context(), apiKey, keyIdle)
			if err != nil {
				writeKeyRefused(w, r, err, user, keyIdle)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, &user)))
		})
	}
}

// writeMissingKey answers a request with no X-API-Key. The most common
// mistake (humans and LLMs alike) is sending the key as `Authorization:
// Bearer <key>`: this API only reads X-API-Key, so point them at the right
// header instead of a bare "unauthorized".
func writeMissingKey(w http.ResponseWriter, r *http.Request) {
	msg := "missing API key: send it as the header 'X-API-Key: <key>'. " +
		"Get a key via POST /v1/auth then POST /v1/auth/verify. See /llms.txt."
	code := "missing_api_key"
	if r.Header.Get("Authorization") != "" {
		msg = "missing X-API-Key header: this API authenticates with " +
			"'X-API-Key: <key>', not 'Authorization: Bearer'. Resend your key " +
			"in the X-API-Key header. See /llms.txt."
		code = "wrong_auth_header"
	}
	httpx.WriteError(w, http.StatusUnauthorized, code, msg)
}

// writeKeyRefused answers a key that does not sign anyone in: a suspended
// account (its key is kept, not deleted, so re-enabling the account brings
// it back; the person sees why here), an expired key, an unknown one.
func writeKeyRefused(w http.ResponseWriter, r *http.Request, err error, user model.User, keyIdle time.Duration) {
	switch {
	case errors.Is(err, model.ErrAccountSuspended):
		note := ""
		if r.URL.Path == "/v1/me/export.zip" || r.URL.Path == "/v1/me/export.tar.gz" {
			// The right of access stands while suspended.
			note = "; write to " + SupportContact + " for a copy of your data"
		}
		WriteSuspended(w, user.SuspendedReason, note)
	case errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle):
		msg, code := ExpiredKeyMessage(err, user.KeyExpiresAt, keyIdle)
		httpx.WriteError(w, http.StatusUnauthorized, code, msg)
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_api_key", InvalidKeyMessage())
	default:
		httpx.WriteInternal(w)
	}
}

// ExpiredKeyMessage is the 401 a stored key that stopped working gets, and its
// code: key_expired (past the expiry chosen at mint) or key_expired_idle
// (unused for KEY_IDLE_EXPIRY_DAYS).
func ExpiredKeyMessage(err error, expiredAt *time.Time, keyIdle time.Duration) (string, string) {
	if errors.Is(err, model.ErrKeyExpired) {
		msg := "this API key has expired"
		if expiredAt != nil {
			msg += " (on " + expiredAt.UTC().Format("2 January 2006") + ")"
		}
		return msg + ". Create a new key from the Keys panel on your Simple Host page, or sign in again via POST /v1/auth.", "key_expired"
	}
	days := int(keyIdle.Hours() / 24)
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

// Optional runs mw (Middleware) only when the request carries an X-API-Key:
// a public route then knows its signed-in caller (GetUser), and a bad,
// expired or suspended key is refused exactly as on an owner route.
func Optional(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		withKey := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") == "" {
				next.ServeHTTP(w, r)
				return
			}
			withKey.ServeHTTP(w, r)
		})
	}
}

// WriteSuspended is the one account_suspended refusal (403) on every surface.
func WriteSuspended(w http.ResponseWriter, reason, note string) {
	httpx.WriteJSON(w, http.StatusForbidden, map[string]string{
		"error": SuspendedMessage(reason) + note, "code": "account_suspended", "reason": reason,
	})
}

// Admin is the one admin-only gate: 401 with no caller, 404 (not 403) to a
// signed-in non-admin so the route does not confirm it exists.
func Admin(w http.ResponseWriter, r *http.Request) bool {
	user := GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return false
	}
	return true
}

// WithUser returns ctx acting as user. Only for a caller that has already
// authorised the switch (the admin's per-site actions run the owner routes as
// the site's owner, handler/adminsite.go).
func WithUser(ctx context.Context, user *model.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) *model.User {
	user, _ := ctx.Value(userContextKey).(*model.User)
	return user
}
