package sites

import (
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/host/model"
)

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
// (host/handler) relies on.
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

// A site moving domains keeps the old one as its address until the new one
// is verified, for the sitemap, REST and MCP alike.
func TestLiveURLDuringDomainSwitch(t *testing.T) {
	s := model.Site{CustomDomain: sql.NullString{String: "new.com", Valid: true}, PreviousDomain: "old.com"}
	if got := LiveURL(s, "https://fam/", "https://plat/"); got != "https://old.com/" {
		t.Fatal(got)
	}
}

// An event name claim takes only what Simple Hack's own check takes.
func TestNormalizeEventNameSharesProductShape(t *testing.T) {
	for _, bad := range []string{"a--b", "ab", "abcdefghij-abcdefghij-abcdefghij-abcdefg"} {
		if _, err := NormalizeEventName(bad); err == nil {
			t.Fatal(bad)
		}
	}
	if n, err := NormalizeEventName(" Meetup "); err != nil || n != "meetup" {
		t.Fatal(n, err)
	}
}

// One domain shape for site domains and the instance's own domain.
func TestDomainShapeRefusesIP(t *testing.T) {
	if DomainShape("1.2.3.4") == nil || DomainShape("-a.com") == nil || DomainShape("shop.example.com") != nil {
		t.Fatal("domain shape")
	}
}
