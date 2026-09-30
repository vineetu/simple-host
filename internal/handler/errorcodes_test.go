package handler

import (
	"net/http"
	"strings"
	"testing"
)

// Refusals that share an HTTP status carry a code saying which refusal it is;
// the MCP tools choose their recovery hint by it. Needs DB_DSN.
func TestRefusalsCarryACode(t *testing.T) {
	a := newPrivateApp(t)
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	const apex = "simple-host.test"
	a.deploy(t, ann, "blog")
	a.deploy(t, ann, "notes")
	files := map[string]any{"files": map[string]string{"index.html": "x"}}

	cases := []struct {
		name, method, path string
		body               any
		headers            map[string]string
		status             int
		code               string
	}{
		{"create over an existing site", "POST", "/v1/sites/blog/files", files, key, http.StatusConflict, "site_exists"},
		{"rename onto an existing site", "PATCH", "/v1/sites/blog", map[string]string{"name": "notes"}, key, http.StatusConflict, "site_exists"},
		{"reserved name", "POST", "/v1/sites/admin/files", files, key, http.StatusBadRequest, "name_reserved"},
		{"invalid name", "POST", "/v1/sites/Bad_Name/files", files, key, http.StatusBadRequest, "invalid_name"},
		{"rename to a reserved name", "PATCH", "/v1/sites/blog", map[string]string{"name": "www"}, key, http.StatusBadRequest, "name_reserved"},
		{"invalid domain", "POST", "/v1/sites/blog/domain", map[string]string{"domain": "not a domain"}, key, http.StatusBadRequest, "invalid_domain"},
		{"reserved free address", "POST", "/v1/sites/blog/domain", map[string]string{"domain": "www." + pcSiteDomain}, key, http.StatusBadRequest, "name_reserved"},
		{"no key", "GET", "/v1/sites", nil, nil, http.StatusUnauthorized, "missing_api_key"},
		{"unknown key", "GET", "/v1/sites", nil, map[string]string{"X-API-Key": "nope"}, http.StatusUnauthorized, "invalid_api_key"},
	}
	for _, c := range cases {
		r := a.at(t, c.method, apex, c.path, c.body, c.headers)
		if r.status != c.status || r.json(t)["code"] != c.code {
			t.Errorf("%s: got %d %s, want %d with code %s", c.name, r.status, r.body, c.status, c.code)
		}
	}
}

// A call about a site the account does not have says how to fix it: a deploy
// names the create call, anything else points at the list of sites. The error
// text and code clients already read are unchanged. Needs DB_DSN.
func TestSiteNotFoundCarriesAHint(t *testing.T) {
	a := newPrivateApp(t)
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	const apex = "simple-host.test"
	files := map[string]any{"files": map[string]string{"index.html": "x"}}

	cases := []struct {
		name, method, path string
		body               any
		code, errText      string
		hint               []string
	}{
		{"JSON deploy", "PUT", "/v1/sites/ghost/files", files, "not_found",
			"site not found: create it with POST, or add ?create=1 to this PUT to create it when missing",
			[]string{"No site named ghost on your account.", "PUT /v1/sites/ghost/files?create=1", "POST /v1/sites/ghost/files."}},
		{"versions", "GET", "/v1/sites/ghost/versions", nil, "site_not_found", "site not found",
			[]string{"No site named ghost on your account.", "GET /v1/sites"}},
		{"version files", "GET", "/v1/sites/ghost/versions/1/files", nil, "site_not_found", "site not found", []string{"GET /v1/sites"}},
		{"rollback", "PUT", "/v1/sites/ghost/active-version", map[string]int{"version_number": 1}, "site_not_found", "site not found", []string{"GET /v1/sites"}},
	}
	for _, c := range cases {
		r := a.at(t, c.method, apex, c.path, c.body, key)
		j := r.json(t)
		if r.status != http.StatusNotFound || j["code"] != c.code || j["error"] != c.errText {
			t.Errorf("%s: got %d %s, want 404 %s %q", c.name, r.status, r.body, c.code, c.errText)
			continue
		}
		hint, _ := j["hint"].(string)
		for _, want := range c.hint {
			if !strings.Contains(hint, want) {
				t.Errorf("%s: hint %q lacks %q", c.name, hint, want)
			}
		}
	}
}
