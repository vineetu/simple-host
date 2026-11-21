package datahttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
)

func TestStorageRulesInvalidHTTPResponses(t *testing.T) {
	w := httptest.NewRecorder()
	WriteStorageError(w, &appstorage.Error{Code: "rules_invalid", Message: "stored rules are invalid"}, 400)
	checkAPIError(t, w, http.StatusForbidden, "rules_invalid")
}
