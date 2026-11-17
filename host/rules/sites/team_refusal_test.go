package sites

import "testing"

func TestTeamRefusalMessage(t *testing.T) {
	cases := map[string]string{
		"event_taken_down":     "this event has been taken down",
		"event_closed":         "this event has ended, so its team sites no longer change",
		"team_site_taken_down": "the organiser has taken your team's site down; ask them about it",
		"submissions_closed":   "the submission deadline has passed, so your team's site and entry are frozen. Ask the organiser if you need more time",
		"unknown":              "your team cannot change its site now",
	}
	for code, want := range cases {
		if got := TeamRefusalMessage(code); got != want {
			t.Errorf("TeamRefusalMessage(%q) = %q, want %q", code, got, want)
		}
	}
}
