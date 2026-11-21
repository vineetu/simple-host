package accesshttp

import (
	"testing"
)

func TestOwnerSignInAlertSettingsLink(t *testing.T) {
	alerts := &SignInAlerts{publicBaseURL: "https://example.test"}
	for handle, want := range map[string]string{
		"owner": "https://example.test/owner#settings",
		"":      "https://example.test/dashboard",
	} {
		if got := alerts.ownerAppURL(handle); got != want {
			t.Errorf("ownerAppURL(%q) = %q, want %q", handle, got, want)
		}
	}
}
