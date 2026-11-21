package datahttp

import (
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
)

func TestStorageKVRulesErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"rules_invalid", 403}, {"internal_error", 500}, {"sign_in_required", 401}, {"account_suspended", 403}, {"forbidden", 403}, {"spam_check_required", 403}, {"spam_check_failed", 403}, {"spam_check_unavailable", 403}} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteStorageError(w, &appstorage.Error{Code: tc.code, Message: "refused"}, 0)
			checkAPIError(t, w, tc.status, tc.code)
		})
	}
}
