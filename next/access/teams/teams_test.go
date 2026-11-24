package teams_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"simplehost/access/teams"
	"simplehost/app"
	"simplehost/core/testkit"
	"simplehost/core/web"
)

// publish is a stand-in for sites/publish: it puts the body live as
// index.html, after the same Of and Manage checks every site route makes.
func publish(a *app.App) {
	a.Routes.Platform.Handle("PUT /v1/sites/{site}", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		s, err := a.Sites.Of(r)
		if err != nil {
			return err
		}
		if err := a.Sites.Manage(r, s); err != nil {
			return err
		}
		dir, err := a.Sites.Stage(s)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "index.html"), []byte("by member"), 0o644)
		}
		if err == nil {
			_, err = a.Sites.Commit(r.Context(), s, dir, web.CallerOf(r).ID)
		}
		return err
	}))
}

func call(t *testing.T, k *testkit.Kit, who testkit.Who, method, path string, body any) testkit.Res {
	t.Helper()
	return testkit.Call(t, k.Browser(), method, "https://simple-host.app"+path, body, "Authorization", "Bearer "+who.Key)
}

// newTeam makes the team crew for ana and invites email.
func newTeam(t *testing.T, k *testkit.Kit, ana testkit.Who, email string) (teamID, inviteID string) {
	t.Helper()
	r := call(t, k, ana, "POST", "/v1/teams", map[string]string{"handle": "crew", "display_name": "The crew"})
	var team struct{ ID string }
	if json.Unmarshal([]byte(r.Body), &team); r.Status != 201 {
		t.Fatalf("create team: %d %s", r.Status, r.Body)
	}
	r = call(t, k, ana, "POST", "/v1/teams/crew/invites", map[string]string{"email": email})
	var inv struct{ ID string }
	if json.Unmarshal([]byte(r.Body), &inv); r.Status != 201 || len(k.Mail.To(email)) != 1 {
		t.Fatalf("invite: %d %s", r.Status, r.Body)
	}
	return team.ID, inv.ID
}

// SPEC test 7 (F5): an invitation makes nobody a member until accepted. Then
// the member publishes to a team site.
func TestInviteThenMemberPublishes(t *testing.T) {
	k := testkit.Start(t, teams.Mount, publish)
	ana, ben := k.Person(t, "ana"), k.Person(t, "ben")
	teamID, invite := newTeam(t, k, ana, ben.Email)
	k.Publish(t, testkit.Who{ID: teamID}, "home", map[string]string{"index.html": "first"})

	if r := call(t, k, ben, "PUT", "/v1/sites/home?owner=crew", nil); r.Status != 403 {
		t.Fatalf("invited, not accepted, publishes: %d %s", r.Status, r.Body)
	}
	if r := call(t, k, ben, "POST", "/v1/invites/"+invite+"/accept", nil); r.Status != 200 {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	if r := call(t, k, ben, "PUT", "/v1/sites/home?owner=crew", nil); r.Status != 200 {
		t.Fatalf("member publishes: %d %s", r.Status, r.Body)
	}
	s, err := k.App.Sites.Get(context.Background(), teamID, "home")
	if err != nil || s.LiveVersion != 2 {
		t.Fatalf("team site after member publish: %+v %v", s, err)
	}
}

// SPEC test 8 (F5): an invitation cannot be accepted by a different email.
func TestInviteOnlyForItsEmail(t *testing.T) {
	k := testkit.Start(t, teams.Mount, publish)
	ana, ben, cat := k.Person(t, "ana"), k.Person(t, "ben"), k.Person(t, "cat")
	teamID, invite := newTeam(t, k, ana, ben.Email)
	k.Publish(t, testkit.Who{ID: teamID}, "home", map[string]string{"index.html": "first"})

	if r := call(t, k, cat, "POST", "/v1/invites/"+invite+"/accept", nil); r.Status != 403 {
		t.Fatalf("another email accepts: %d %s", r.Status, r.Body)
	}
	if r := call(t, k, cat, "PUT", "/v1/sites/home?owner=crew", nil); r.Status != 403 {
		t.Fatalf("non-member publishes: %d %s", r.Status, r.Body)
	}
	if r := call(t, k, ben, "POST", "/v1/invites/"+invite+"/accept", nil); r.Status != 200 {
		t.Fatalf("the invited email accepts: %d %s", r.Status, r.Body)
	}
}
