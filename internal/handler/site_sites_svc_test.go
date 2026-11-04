package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/internal/app/sites"
)

func TestWriteSitesError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"site lookup", &sites.Error{Code: "site_not_found"}, http.StatusNotFound, "{\"error\":\"site not found\",\"code\":\"site_not_found\",\"hint\":\"No site named example on your account. GET /v1/sites lists the sites on your account; to create one, POST /v1/sites/\\u003cname\\u003e/files.\"}\n"},
		{"missing row", &sites.Error{Code: "not_found", Message: "site not found"}, http.StatusNotFound, "{\"error\":\"site not found\"}\n"},
		{"marker", &sites.Error{Code: "marker_failed", Message: "saved, but the site's files could not be marked; try again"}, http.StatusInternalServerError, "{\"error\":\"saved, but the site's files could not be marked; try again\"}\n"},
		{"unexpected", errors.New("private details"), http.StatusInternalServerError, "{\"error\":\"internal server error\"}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeSitesError(w, tc.err, "example")
			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Fatalf("got %d %q, want %d %q", w.Code, w.Body.String(), tc.status, tc.body)
			}
		})
	}
}
