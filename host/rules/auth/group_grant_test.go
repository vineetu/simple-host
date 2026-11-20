package auth

import "testing"

func TestEventsGrant(t *testing.T) {
	for scope, want := range map[string]bool{
		"sites":        false,
		"sites events": true,
		"events sites": true,
		"events":       false,
		"sites team:00000000-0000-4000-8000-000000000000": false,
		"sites eventsx":       false,
		"sites Events":        false,
		"sites events events": false,
	} {
		if got := EventsGrant(scope); got != want {
			t.Errorf("%q: got %v, want %v", scope, got, want)
		}
	}
}
