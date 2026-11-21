package hackhttp

import "testing"

// Turning approval off and admitting pending admits join-link sign-ups only;
// a formal application stays pending.
func TestRhpAdmitPendingLinkOnly(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "rhpaplorg")
	linkPerson := a.newPerson(t, "rhpaplink")
	appPerson := a.newPerson(t, "rhpaplapp")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)
	base := "/v1/hack/events/" + slug

	wantCode(t, "approval on", a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, a.key(org)), 200, "")
	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantCode(t, "link join", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "L"}, a.key(linkPerson)), 200, "")
	wantCode(t, "apply", a.apply(t, slug, appPerson, nil), 200, "")
	wantCode(t, "admit pending", a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false, "admit_pending": true}, a.key(org)), 200, "")

	for who, want := range map[string]string{"link": "approved", "application": "pending"} {
		p := linkPerson
		if who == "application" {
			p = appPerson
		}
		var got string
		if err := a.database.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, p)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s status after admit_pending = %q, want %q", who, got, want)
		}
	}
}

// Moving the last admin to another team promotes someone on the old team and
// does not carry the admin rank to the new team.
func TestRhpTeamAdminSuccessionOnOrganiserMove(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "rhpsmorg")
	leader := a.newPerson(t, "rhpsmleader")
	member := a.newPerson(t, "rhpsmmember")
	third := a.newPerson(t, "rhpsmthird")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member, third} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	gammaID, _ := a.createTeamAndJoin(t, slug, third, "Gamma")
	base := "/v1/hack/events/" + slug
	wantCode(t, "organiser moves leader", a.at(t, "POST", base+"/teams/"+a.teamSlugByID(t, gammaID)+"/members", map[string]any{"user_id": a.userID(t, leader)}, a.key(org)), 200, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin of the old team")
	}
	if a.teamOf(t, leader) != gammaID {
		t.Fatal("leader was not moved")
	}
	if a.isTeamAdmin(t, leader) {
		t.Fatal("leader kept admin on the new team")
	}
}
