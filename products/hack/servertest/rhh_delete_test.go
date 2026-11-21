package servertest

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// TestRhhDeleteKeepsNameOfJoinedMemberWithoutTeams: a member who joined before
// names were reserved on join still keeps the event's name reserved.
func TestRhhDeleteKeepsNameOfJoinedMemberWithoutTeams(t *testing.T) {
	for _, role := range []string{"participant", "judge"} {
		t.Run(role, func(t *testing.T) {
			a := newTeamSiteApp(t)
			org, p := a.newPerson(t, "rhh-name-org"), a.newPerson(t, "rhh-name-person")
			slug := a.makeEvent(t, org)
			base := "/v1/hack/events/" + slug
			t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug) })
			if role == "participant" {
				a.join(t, slug, p, org)
			} else {
				code := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)["judge_code"].(string)
				wantTS(t, "judge join", a.api(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true, "display_name": "J"}, p.key), 200, "")
			}
			if _, err := a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug); err != nil {
				t.Fatal(err)
			}
			wantTS(t, "remove person", a.api(t, "DELETE", base+"/people/"+a.uid(t, p), nil, org.key), 204, "")
			wantTS(t, "empty deletion", a.api(t, "DELETE", base, nil, org.key), 204, "")
			taken, err := hackevents.EventSlugTaken(context.Background(), a.database, slug)
			if err != nil || !taken {
				t.Fatalf("joined name released: %v", err)
			}
		})
	}
}
