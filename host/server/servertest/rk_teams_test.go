package servertest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// rkTeam has p create a team named after label, returning its handle.
func rkTeam(t *testing.T, a *PrivateApp, p Person, label string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%d", label, time.Now().UnixNano()%1e11)
	r := a.At(t, "POST", PcSiteDomain, "/v1/teams", map[string]string{"name": name}, rkKey(p.Key))
	if r.Status != http.StatusCreated || r.JSON(t)["handle"] != name {
		t.Fatalf("create team: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE handle = $1 AND is_team`, name) })
	return name
}

// rkAddMember has by invite who by email, and who accept.
func rkAddMember(t *testing.T, a *PrivateApp, by Person, team string, who Person) {
	t.Helper()
	if r := a.At(t, "POST", PcSiteDomain, "/v1/teams/"+team+"/members", map[string]string{"email": who.Email}, rkKey(by.Key)); r.Status != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.Status, r.Body)
	}
	me := a.At(t, "GET", PcSiteDomain, "/v1/me", nil, rkKey(who.Key))
	var id string
	list, _ := me.JSON(t)["team_invitations"].([]any)
	for _, v := range list {
		if v := v.(map[string]any); v["team"] == team {
			id = v["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("no invitation to %s: %s", team, me.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/me/team-invitations/"+id, map[string]string{"answer": "accept"}, rkKey(who.Key)); r.Status != http.StatusOK {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
}

// rkOwnerDeploy publishes a site as owner= (a team, or an account the key
// is an editor of).
func rkOwnerDeploy(t *testing.T, a *PrivateApp, p Person, owner, site string) Resp {
	t.Helper()
	return a.At(t, "PUT", PcSiteDomain, "/v1/sites/"+site+"/files?create=1&owner="+owner, rkFiles("<h1>"+site+"</h1>"), rkKey(p.Key))
}

// rkNewEditor has ann make a new person an editor of her site "shop".
func rkNewEditor(t *testing.T, a *PrivateApp, ann Person) (bea Person, annHandle string) {
	t.Helper()
	bea = a.NewPerson(t, "editor")
	_, beaHandle := a.UserID(t, bea)
	_, annHandle = a.UserID(t, ann)
	r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/editors", map[string]string{"person": beaHandle}, rkKey(ann.Key))
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), beaHandle) {
		t.Fatalf("add editor: %d %s", r.Status, r.Body)
	}
	return bea, annHandle
}

// A site's editor creates and writes the site's owner-only storage with their
// own key and owner=<account>, the owner reads what they wrote, and the
// editor follows the site to its new name after a rename; a stranger
// naming the account is refused.
func TestRkSiteEditorOwnsTheSiteStorageAndFollowsARename(t *testing.T) {
	a, _ := NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-store")
	bea, annHandle := rkNewEditor(t, a, ann)
	stranger := a.NewPerson(t, "editor-stranger")
	if r := rkOwnerDeploy(t, a, ann, annHandle, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	owner := "?owner=" + annHandle

	// Rules-less resource: owner-only. The editor creates it and writes it.
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/resources/notes"+owner, map[string]string{"kind": "kv"}, rkKey(bea.Key)); r.Status != http.StatusCreated {
		t.Fatalf("editor creates resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k"+owner, `{"value":"editors too"}`, rkKey(bea.Key)); r.Status != http.StatusOK {
		t.Fatalf("editor writes: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k", nil, rkKey(ann.Key)); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "editors too") {
		t.Fatalf("owner reads the editor's value: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k"+owner, `{"value":"intruder"}`, rkKey(stranger.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("stranger writes with owner=: %d %s", r.Status, r.Body)
	}

	// The editor follows the site to its new name.
	if r := a.At(t, "PATCH", PcSiteDomain, "/v1/sites/shop", map[string]any{"name": "store"}, rkKey(ann.Key)); r.Status != http.StatusOK {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/store/files?create=1&owner="+annHandle, rkFiles("<h1>renamed</h1>"), rkKey(bea.Key)); r.Status != http.StatusOK {
		t.Fatalf("editor publishes the renamed site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/store/storage/kv/notes/keys/k2"+owner, `{"value":"after rename"}`, rkKey(bea.Key)); r.Status != http.StatusOK {
		t.Fatalf("editor writes the renamed site's storage: %d %s", r.Status, r.Body)
	}
}

// A team member creates and writes the team's site storage with their own
// key and owner=<team> and, signed in on the site, is the site's owner there;
// a non-member is refused on both paths, and so is the member once removed.
func TestRkTeamMemberWritesTheTeamsSiteStorage(t *testing.T) {
	a, cert := NewSiteApp(t, "canonical")
	ann, ben, cal := a.NewPerson(t, "team-store"), a.NewPerson(t, "team-store-b"), a.NewPerson(t, "team-store-c")
	team := rkTeam(t, a, ann, "store")
	rkAddMember(t, a, ann, team, ben)
	if r := rkOwnerDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	MarkReady(t, cert, team)
	host := "shop." + team + "." + PcSiteDomain
	var sid string
	if err := a.Database.QueryRow(`SELECT s.id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1 AND s.name = 'shop'`, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	owner := "?owner=" + team

	// Rules-less resource: owner-only. The member's key with owner= writes it.
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/resources/notes"+owner, map[string]string{"kind": "kv"}, rkKey(ben.Key)); r.Status != http.StatusCreated {
		t.Fatalf("member creates resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k"+owner, `{"value":"members only"}`, rkKey(ben.Key)); r.Status != http.StatusOK {
		t.Fatalf("member writes with key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k"+owner, `{"value":"outsider"}`, rkKey(cal.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("non-member writes with owner=: %d %s", r.Status, r.Body)
	}
	benSite, calSite := Browser(host, a.Session(t, ben, sid, host)), Browser(host, a.Session(t, cal, sid, host))
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.Status != http.StatusOK {
		t.Fatalf("member signed in on the site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/me", nil, benSite); r.JSON(t)["site_owner"] != true {
		t.Fatalf("member is not the site's owner there: %s", r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, calSite); r.Status != http.StatusForbidden {
		t.Fatalf("non-member signed in on the site: %d %s", r.Status, r.Body)
	}
	_, benHandle := a.UserID(t, ben)
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/teams/"+team+"/members/"+benHandle, nil, rkKey(ann.Key)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.Status != http.StatusForbidden {
		t.Fatalf("removed member's next on-site request: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/kv/notes/keys/k"+owner, `{"value":"gone"}`, rkKey(ben.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("removed member writes with owner=: %d %s", r.Status, r.Body)
	}
}
