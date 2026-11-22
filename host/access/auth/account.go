package auth

import (
	"errors"
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// AccountKeyUser returns the signed-in person, and refuses (400
// not_an_account_key) a request that did not come with one of the account's
// stored keys: the env admin key, or a connected app acting for the person.
// A connected app must not turn its grant into a key that outlives it.
func AccountKeyUser(w http.ResponseWriter, r *http.Request, what string) *model.User {
	user := GetUser(r.Context())
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

// WriteLifecycleFailure answers err when it is not nil: a LifecycleError with
// its status and words, anything else as an internal error. It reports
// whether it answered.
func WriteLifecycleFailure(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var failure *accounts.LifecycleError
	if errors.As(err, &failure) {
		httpx.WriteJSON(w, failure.Status, httpx.ErrorResponse{Error: SupportText(failure.Message), Code: failure.Code})
	} else {
		httpx.WriteInternal(w)
	}
	return true
}

// SupportText is s with this install's support contact in place of the
// hosted one.
func SupportText(s string) string {
	if SupportContact == "support@simple-host.app" {
		return s
	}
	return strings.ReplaceAll(s, "support@simple-host.app", SupportContact)
}
