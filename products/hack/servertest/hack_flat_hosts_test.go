package servertest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

// The platform half covers label parsing and dispatch using a group stub.
// This half proves real event lookup wins over a colliding flat team address.
func TestHackFlatHostsRealEventWins(t *testing.T) {
	a := newTeamSiteApp(t)
	a.sites.SetTeamSiteFlatHosts(true)
	org, member := a.newPerson(t, "flat-org"), a.newPerson(t, "flat-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Alpha", member)
	wantTS(t, "publish", a.deployTeam(t, team, a.teamKey(t, slug, member), "team content"), http.StatusCreated, "")
	flat := team + "--" + slug
	r := a.on(t, "GET", flat+"."+tsDomain, "/", nil, "")
	if r.status != 200 || !strings.Contains(string(r.body), "team content") {
		t.Fatalf("flat team: %d %s", r.status, r.body)
	}
	otherOrg := a.newPerson(t, "legacy-flat-org")
	legacy := a.makeEvent(t, otherOrg)
	var account string
	if err := a.database.QueryRow(`UPDATE events SET slug=$1 WHERE slug=$2 RETURNING account_id`, flat, legacy).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET handle=$1 WHERE id=$2`, flat, account); err != nil {
		t.Fatal(err)
	}
	a.sites.SetGroupHosts(hackhttp.NewGroupHosts(a.database, func(w http.ResponseWriter, r *http.Request, u model.User) bool {
		if u.ID != account {
			return false
		}
		w.Write([]byte("legacy event page"))
		return true
	}, nil))
	r = a.on(t, "GET", flat+"."+tsDomain, "/", nil, "")
	if r.status != 200 || string(r.body) != "legacy event page" {
		t.Fatalf("legacy event: %d %s", r.status, r.body)
	}
	g, err := hackhttp.NewGroupHosts(a.database, nil, nil).ByAccount(context.Background(), account)
	if err != nil || g.Slug != flat {
		t.Fatalf("group lookup: %+v %v", g, err)
	}
}
