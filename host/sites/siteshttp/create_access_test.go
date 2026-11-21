package siteshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
)

func TestCreateAccessValidation(t *testing.T) {
	h := withHosts(&SiteHandler{})
	h.Limits = config.DefaultLimits()
	h.Limits.SiteViewersMax = 1
	for _, tc := range []struct {
		access  string
		viewers []string
		code    string
	}{
		{"bogus", nil, "invalid_request"}, {"anyone", []string{}, "invalid_request"},
		{"specific", []string{"bad"}, "invalid_email"}, {"specific", []string{"a@example.com", "b@example.com"}, "too_many_viewers"},
	} {
		r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
		w := httptest.NewRecorder()
		if h.readCreateAccess(w, r, &tc.access, tc.viewers) || w.Code != 400 || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body)
		}
	}
	access := "specific"
	r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
	if !h.readCreateAccess(httptest.NewRecorder(), r, &access, []string{" A@Example.com ", "a@example.com"}) || len(creationAccess(r).Viewers) != 1 {
		t.Fatal("normalization/dedupe")
	}
}

func TestCreateAccessArchiveOptions(t *testing.T) {
	h := withHosts(&SiteHandler{})
	h.Limits = config.DefaultLimits()
	for _, path := range []string{
		"/v1/sites/s?access=specific&viewers=A@example.com,b@example.com&viewers=a@example.com",
		"/v1/sites/s?access=specific&viewers=A@example.com&viewers=b@example.com",
	} {
		r := httptest.NewRequest("POST", path, nil)
		if !h.readArchiveCreateAccess(httptest.NewRecorder(), r) || len(creationAccess(r).Viewers) != 2 {
			t.Fatal("archive viewer parsing")
		}
	}
	r := httptest.NewRequest("PUT", "/v1/sites/s?access=specific", nil)
	w := httptest.NewRecorder()
	if h.readArchiveCreateAccess(w, r) || w.Code != 400 {
		t.Fatal("plain PUT accepted access")
	}
	// A query that names access but does not parse never falls back to open.
	for _, path := range []string{"/v1/sites/s?access=specific;viewers=a@example.com", "/v1/sites/s?access=specific%ZZ"} {
		r := &http.Request{Method: "POST", URL: &url.URL{Path: "/v1/sites/s", RawQuery: strings.SplitN(path, "?", 2)[1]}}
		w := httptest.NewRecorder()
		if h.readArchiveCreateAccess(w, r) || w.Code != 400 {
			t.Fatalf("malformed query %s accepted: %d", path, w.Code)
		}
	}
	// Field names match without regard to case, null included.
	for _, data := range []string{`{"Access":null}`, `{"ACCESS":null}`, `{"Viewers":null}`} {
		var body filesRequest
		if err := json.Unmarshal([]byte(data), &body); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
		if h.readCreateAccess(httptest.NewRecorder(), r, body.Access, body.Viewers) {
			t.Fatalf("%s treated as omitted", data)
		}
	}
	var upper filesRequest
	if err := json.Unmarshal([]byte(`{"Access":"specific","VIEWERS":["a@example.com"]}`), &upper); err != nil || upper.Access == nil || *upper.Access != "specific" || len(upper.Viewers) != 1 {
		t.Fatalf("case-insensitive fields: %v %+v", err, upper)
	}
	for _, data := range []string{`{"access":null}`, `{"viewers":null}`, `{"access":"anyone","viewers":[]}`} {
		var body filesRequest
		if err := json.Unmarshal([]byte(data), &body); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "/v1/sites/s/files", nil)
		if h.readCreateAccess(httptest.NewRecorder(), r, body.Access, body.Viewers) {
			t.Fatalf("plain PUT ignored %s", data)
		}
	}
}
