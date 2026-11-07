package hackhttp

import (
	"net/http"
	"testing"
)

func TestNavigationAndActorConflicts(t *testing.T) {
	w := newRulesWorld(t, 1)
	a := w.a
	base := "/v1/hack/events/" + w.slug
	teamID := w.teamID(t, w.teams[0].slug)
	r := a.at(t, "POST", base+"/conflicts", map[string]any{"team_id": teamID}, a.key(w.judge))
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("declare: %d %s", r.status, r.body)
	}
	for _, tc := range []struct {
		who   person
		query string
		count int
	}{{w.org, "", 1}, {w.org, "?mine=1", 0}, {w.judge, "?mine=1", 1}, {w.judge, "", 1}} {
		r := a.at(t, "GET", base+"/conflicts"+tc.query, nil, a.key(tc.who))
		if r.status != 200 {
			t.Fatalf("read: %d %s", r.status, r.body)
		}
		if got := len(r.json(t)["conflicts"].([]any)); got != tc.count {
			t.Fatalf("%s %s got %d want %d", tc.who.email, tc.query, got, tc.count)
		}
	}
	for _, tc := range []struct {
		who                     person
		manage, judge, takePart bool
	}{{w.org, true, true, false}, {w.judge, false, true, false}, {w.teams[0].person, false, false, true}} {
		r := a.at(t, "GET", base, nil, a.key(tc.who))
		can := r.json(t)["can"].(map[string]any)
		if can["manage"] != tc.manage || can["judge"] != tc.judge || can["take_part"] != tc.takePart {
			t.Fatalf("can: %v", can)
		}
	}
	r = a.at(t, "GET", "/v1/hack/navigation", nil, a.key(w.org))
	if r.status != 200 || r.json(t)["version"] != float64(1) {
		t.Fatalf("map: %d %s", r.status, r.body)
	}
}
