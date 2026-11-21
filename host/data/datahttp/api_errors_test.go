package datahttp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
)

func checkAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) httpx.ErrorResponse {
	t.Helper()
	var body httpx.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %s: %v", rec.Body.String(), err)
	}
	if rec.Code != status || body.Code != code || body.Error == "" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("got %d %s (%s), want %d %s", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), status, code)
	}
	return body
}
