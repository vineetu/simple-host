package handler

import (
	"net/http"
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
