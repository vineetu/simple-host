package datahttp

import (
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
)

// The storage error code decides the HTTP status; spam-check refusals are
// 403 so a bot cannot tell them from a rule refusal.
func TestWriteStorageErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"rules_invalid", 403}, {"sign_in_required", 401}, {"spam_check_failed", 403}, {"query_too_expensive", 422}, {"storage_busy", 503}, {"internal_error", 500}} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteStorageError(w, &appstorage.Error{Code: tc.code, Message: "refused"}, 0)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if tc.code == "storage_busy" && w.Header().Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
		})
	}
}
