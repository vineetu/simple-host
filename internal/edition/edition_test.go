package edition

import (
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

func TestNew(t *testing.T) {
	for _, tc := range []struct {
		events string
		want   Kind
		routes RoutePolicy
		pres   PresentationPolicy
	}{
		{"hosted", Hack, RoutePolicy{HostedEvents: true}, PresentationPolicy{ProductName: "Simple Hack", HackAssets: true}},
		{"off", Host, RoutePolicy{}, PresentationPolicy{ProductName: "Simple Host"}},
		{"", Host, RoutePolicy{}, PresentationPolicy{ProductName: "Simple Host"}},
	} {
		e := New(config.Config{Events: tc.events})
		if e.Identity() != (map[Kind]IdentityPolicy{Host: {PersonalSites: true}, Hack: {RandomHandles: true, HoldingAccounts: true, TeamKeys: true, PaperMetrics: true}})[tc.want] {
			t.Errorf("Events %q: unexpected identity policy: %+v", tc.events, e.Identity())
		}
		if e.Kind() != tc.want || e.Routes() != tc.routes || e.Presentation() != tc.pres {
			t.Errorf("Events %q: got %v %+v %+v", tc.events, e.Kind(), e.Routes(), e.Presentation())
		}
	}
	var nilEd *Edition
	if nilEd.Kind() != Host || nilEd.Routes().HostedEvents {
		t.Error("nil Edition must behave as Host")
	}
}
