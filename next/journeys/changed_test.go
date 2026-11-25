package journeys_test

import (
	"strings"
	"testing"

	"simplehost/core/testkit"
)

// An address of the service before it was rebuilt says so, and what to do.
func TestChangedAddresses(t *testing.T) {
	k := testkit.Start(t, all...)
	b := k.Browser()
	for _, c := range []struct {
		method, path string
		status       int
		has          string
	}{
		{"GET", "/v1/me/keys", 404, platform + "/llms.txt"},
		{"POST", "/v1/me", 405, platform + "/skills.zip"},
		{"POST", "/v1/sites/blog/files", 405, "Publish with PUT /v1/sites/blog"},
	} {
		r := testkit.Call(t, b, c.method, platform+c.path, map[string]any{})
		if r.Status != c.status || !strings.Contains(r.Body, `"code":"changed"`) || !strings.Contains(r.Body, "rebuilt") || !strings.Contains(r.Body, c.has) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}
