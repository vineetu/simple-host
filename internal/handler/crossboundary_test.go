package handler

import (
	"testing"
)

// wantTS is hack_m2_test.go's own (internal/hackhttp now), duplicated here
// (cleanup 3.2 slice 3): imagescan_test.go, notify_site_events_test.go and
// sso_test.go all need it.
func wantTS(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: got %d want %d: %s", what, r.status, status, r.body)
	}
	if code != "" {
		if got, _ := r.json(t)["code"].(string); got != code {
			t.Fatalf("%s: code %q want %q: %s", what, got, code, r.body)
		}
	}
}

// deployBody is hack_m2_test.go's own (internal/hackhttp now); duplicated
// here for the same reason as the rest of this file.
func deployBody(text string) map[string]any {
	return map[string]any{"files": map[string]string{"index.html": "<h1>" + text + "</h1>"}}
}
