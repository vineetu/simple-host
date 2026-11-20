package servertest

import (
	"strings"
	"testing"
)

// Deleting a participant's account erases their own rows; the event's
// account, its team site and the teammate stay.
func TestAccountErasureKeepsEventData(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, mate := a.newPerson(t, "org"), a.newPerson(t, "member"), a.newPerson(t, "mate")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, mate, org)
	team, code := a.startTeam(t, slug, "Erasure", member)
	wantTS(t, "mate joins", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, mate.key), 200, "")
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy", a.deployTeam(t, slug, team, member.key, "kept"), 201, "")
	id := a.uid(t, member)
	handle := strings.Split(member.email, "@")[0]
	wantTS(t, "delete account", a.api(t, "DELETE", "/v1/me", map[string]string{"confirm": handle}, member.key), 204, "")
	for _, q := range []string{
		`SELECT count(*) FROM users WHERE id=$1`,
		`SELECT count(*) FROM event_members WHERE user_id=$1`,
		`SELECT count(*) FROM site_editors WHERE user_id=$1`,
	} {
		var n int
		if err := a.database.QueryRow(q, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("erasure rows=%d err=%v for %s", n, err, q)
		}
	}
	if r := a.on(t, "GET", team+"."+slug+"."+tsDomain, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "kept") {
		t.Fatalf("team site after erasure: %d %s", r.status, r.body)
	}
	wantTS(t, "teammate still publishes", a.deployTeam(t, slug, team, mate.key, "mate"), 200, "")
}
