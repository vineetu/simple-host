package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// A refusal's code picks the hint, so a 409 that means "address taken" is not
// answered with the hint for "site already exists".
func TestRestErrorHintFollowsTheCode(t *testing.T) {
	cases := []struct {
		status      int
		body        string
		want, avoid string
	}{
		{409, `{"error":"that address is taken by another site; pick another name","code":"domain_taken"}`, "belongs to another site", "update_site"},
		{409, `{"error":"public lists are append-only","code":"append_only"}`, "cannot be edited or deleted", "update_site"},
		{409, `{"error":"private lists need the site on its own domain","code":"custom_domain_required"}`, "connect_domain", "update_site"},
		{409, `{"error":"site already exists","code":"site_exists"}`, "update_site", ""},
		{400, `{"error":"site name is reserved","code":"name_reserved"}`, "reserved", "correct the arguments"},
		{400, `{"error":"invalid site name","code":"invalid_name"}`, "lowercase letters", "correct the arguments"},
		{403, `{"error":"this site has been taken down","code":"site_suspended"}`, "taken this site down", "not allowed"},
		{403, `{"error":"account suspended","code":"account_suspended"}`, "support@simple-host.app", "not allowed"},
		{401, `{"error":"this site saves on its own domain","code":"use_custom_domain","domain":"shop.example.com"}`, "shop.example.com", "reconnect"},
		// No code, or one this table does not know: the status decides.
		{409, `{"error":"something else"}`, "change the request", "update_site"},
		{409, `{"error":"x","code":"some_future_code"}`, "change the request", ""},
		{429, `{"error":"rate limit exceeded, slow down"}`, "Rate limited", ""},
		{404, `{"error":"site not found"}`, "list_sites", ""},
	}
	for _, c := range cases {
		msg := restError("tool", upstreamResult{status: c.status, header: http.Header{}, body: []byte(c.body)}).Error()
		if !strings.Contains(msg, c.want) {
			t.Errorf("%d %s: hint lacks %q:\n%s", c.status, c.body, c.want, msg)
		}
		if c.avoid != "" && strings.Contains(msg, c.avoid) {
			t.Errorf("%d %s: hint carries %q:\n%s", c.status, c.body, c.avoid, msg)
		}
	}
}

// Every code the handlers send has a hint here, so none falls back silently.
func TestEveryKnownCodeHasAHint(t *testing.T) {
	for _, code := range []string{"site_exists", "domain_taken", "invalid_name", "name_reserved", "invalid_domain",
		"site_quota_reached", "append_only", "custom_domain_required", "private_visitor_only", "private_needs_own_domain",
		"use_custom_domain", "visitor_auth_required", "not_an_object", "not_found", "missing_api_key", "invalid_api_key",
		"invalid_token", "site_suspended", "account_suspended"} {
		if codeHint(code) == "" {
			t.Errorf("no hint for %s", code)
		}
	}
}
