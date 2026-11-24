package teams_test

import (
	"testing"

	"simplehost/access/teams"
	"simplehost/core/testkit"
)

// Review A5: an accepted invitation is spent; removing a member ends their
// open invitations to the team.
func TestInvitationIsSpent(t *testing.T) {
	k := testkit.Start(t, teams.Mount)
	ana, ben := k.Person(t, "ana"), k.Person(t, "ben")
	_, invite := newTeam(t, k, ana, ben.Email)
	if r := call(t, k, ben, "POST", "/v1/invites/"+invite+"/accept", nil); r.Status != 200 {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	call(t, k, ana, "POST", "/v1/teams/crew/invites", map[string]string{"email": ben.Email}) // a second, still open
	if r := call(t, k, ana, "DELETE", "/v1/teams/crew/members/"+ben.ID, nil); r.Status != 200 {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := call(t, k, ben, "POST", "/v1/invites/"+invite+"/accept", nil); r.Status != 404 {
		t.Fatalf("re-accept a spent invitation: %d %s", r.Status, r.Body)
	}
	var open int
	k.App.DB.QueryRow(`SELECT count(*) FROM team_invites WHERE email = $1 AND accepted_at IS NULL`, ben.Email).Scan(&open)
	if open != 0 {
		t.Fatalf("%d open invitations left after removal", open)
	}
}
