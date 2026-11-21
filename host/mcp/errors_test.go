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
		{409, `{"error":"site already exists","code":"site_exists"}`, "update_site", ""},
		{403, `{"error":"this site has been taken down","code":"site_suspended"}`, "taken this site down", "not allowed"},
		// No code, or one this table does not know: the status decides.
		{409, `{"error":"x","code":"some_future_code"}`, "change the request", ""},
		{404, `{"error":"site not found","code":"site_not_found","hint":"No site named x on your account. GET /v1/sites lists the sites on your account."}`, "list_sites", "GET /v1/sites"},
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

func TestRateLimitRetryAfter(t *testing.T) {
	for _, tc := range []struct{ header, want string }{{"120", "Wait 120 seconds"}, {"invalid", "Wait a minute"}} {
		msg := restError("tool", upstreamResult{status: 429, header: http.Header{"Retry-After": []string{tc.header}}, body: []byte(`{"error":"rate limited"}`)}).Error()
		if !strings.Contains(msg, tc.want) {
			t.Errorf("Retry-After %q: %s", tc.header, msg)
		}
	}
}

func TestCredentialHintsAskForReconnect(t *testing.T) {
	for _, code := range []string{"key_expired", "key_expired_idle", "site_locked"} {
		hint := resourceCodeHint(code, "")
		if !strings.Contains(hint, "reconnect Simple Host in their app; never ask for a key in chat") {
			t.Errorf("%s: %s", code, hint)
		}
	}
}
