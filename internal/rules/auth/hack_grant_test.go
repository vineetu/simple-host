package auth

import "testing"

func TestHackGrantKind(t *testing.T) {
	team := "00000000-0000-4000-8000-000000000000"
	cases := []struct {
		scope      string
		teamID     string
		events, ok bool
	}{
		{"sites", "", false, false},
		{"sites events", "", true, true},
		{"events", "", false, false},
		{"sites team:" + team, team, false, true},
		{"sites team:" + team + " events", "", false, false},
		{"sites team:", "", false, false},
		{"sites eventsx", "", false, false},
		{"sites myevents", "", false, false},
		{"sites Events", "", false, false},
		{"sites team: events", "", false, false},
		{"sites team:not-a-uuid", "", false, false},
		{"sites team:" + team + " team:" + team, "", false, false},
		{"sites events events", "", false, false},
		{"events sites", "", true, true},
	}
	for _, c := range cases {
		teamID, events, ok := GrantKind(c.scope)
		if teamID != c.teamID || events != c.events || ok != c.ok {
			t.Errorf("%q: got team=%q events=%v ok=%v, want team=%q events=%v ok=%v", c.scope, teamID, events, ok, c.teamID, c.events, c.ok)
		}
	}
}
