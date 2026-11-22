package servertest

import (
	"net/http"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

func TestNavigationAndActorConflicts(t *testing.T) {
	w := newFeatRulesWorld(t, 1)
	a := w.a
	base := "/v1/hack/events/" + w.Slug
	teamID := w.TeamID(t, w.Teams[0].Slug)
	r := a.At(t, "POST", base+"/conflicts", map[string]any{"team_id": teamID}, a.Key(w.Judge))
	if r.Status != http.StatusCreated && r.Status != http.StatusOK {
		t.Fatalf("declare: %d %s", r.Status, r.Body)
	}
	for _, tc := range []struct {
		who   hacktest.Person
		query string
		count int
	}{{w.Org, "", 1}, {w.Org, "?mine=1", 0}, {w.Judge, "?mine=1", 1}, {w.Judge, "", 1}} {
		r := a.At(t, "GET", base+"/conflicts"+tc.query, nil, a.Key(tc.who))
		if r.Status != 200 {
			t.Fatalf("read: %d %s", r.Status, r.Body)
		}
		if got := len(r.JSON(t)["conflicts"].([]any)); got != tc.count {
			t.Fatalf("%s %s got %d want %d", tc.who.Email, tc.query, got, tc.count)
		}
	}
	for _, tc := range []struct {
		who                     hacktest.Person
		manage, judge, takePart bool
	}{{w.Org, true, false, false}, {w.Judge, false, true, false}, {w.Teams[0].Person, false, false, true}} {
		r := a.At(t, "GET", base, nil, a.Key(tc.who))
		can := r.JSON(t)["can"].(map[string]any)
		if can["manage"] != tc.manage || can["judge"] != tc.judge || can["take_part"] != tc.takePart {
			t.Fatalf("can: %v", can)
		}
	}
	r = a.At(t, "GET", "/v1/hack/navigation", nil, a.Key(w.Org))
	if r.Status != 200 || r.JSON(t)["version"] != float64(1) {
		t.Fatalf("map: %d %s", r.Status, r.Body)
	}
}
