package sites

import "testing"

func TestTeamSiteHost(t *testing.T) {
	if got := TeamSiteHost("Weekend", "Alpha", "Simple-Hack.Test", false); got != "alpha.weekend.simple-hack.test" {
		t.Errorf("two-level = %q", got)
	}
	if got := TeamSiteHost("Weekend", "Alpha", "Simple-Hack.Test", true); got != "alpha--weekend.simple-hack.test" {
		t.Errorf("flat = %q", got)
	}
}

func TestTeamSiteHostLabels(t *testing.T) {
	cases := []struct {
		label       string
		team, event string
		ok          bool
	}{
		{"alpha--weekend", "alpha", "weekend", true},
		{"alpha", "", "", false}, // no "--" at all
		// A legacy event slug containing "--" is accepted as the event,
		// split at the FIRST "--": the team ("alpha") can never contain
		// "--" itself (DeriveTeamSlug), so this is unambiguous.
		{"alpha--weekend--extra", "alpha", "weekend--extra", true},
		{"--weekend", "", "", false}, // empty team
		{"alpha--", "", "", false},   // empty event
		{"", "", "", false},
	}
	for _, c := range cases {
		team, event, ok := TeamSiteHostLabels(c.label)
		if ok != c.ok || (ok && (team != c.team || event != c.event)) {
			t.Errorf("TeamSiteHostLabels(%q) = %q, %q, %v; want %q, %q, %v", c.label, team, event, ok, c.team, c.event, c.ok)
		}
	}
}

// TestTeamSiteHostRoundTrip: whatever TeamSiteHost builds in flat mode,
// TeamSiteHostLabels recovers exactly — the pairing host parsing
// (internal/handler) relies on.
func TestTeamSiteHostRoundTrip(t *testing.T) {
	for _, c := range []struct{ event, team string }{
		{"weekend", "alpha"}, {"spring-hack-2026", "team-one"}, {"a", "b"},
	} {
		host := TeamSiteHost(c.event, c.team, "simple-hack.test", true)
		label := host[:len(host)-len(".simple-hack.test")]
		team, event, ok := TeamSiteHostLabels(label)
		if !ok || team != c.team || event != c.event {
			t.Errorf("round trip %+v: label %q -> %q, %q, %v", c, label, team, event, ok)
		}
	}
}
