package accesstest

import (
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"strings"
	"testing"
)

// Site editors (TM-S2): a person who publishes one site of an account with
// their own key and owner=<account>, without belonging to it.

// editors calls the editors routes of site as by, acting for owner when set.
func editors(t *testing.T, a *servertest.PrivateApp, by servertest.Person, method, site, owner, who string) servertest.Resp {
	t.Helper()
	path, body := "/v1/sites/"+site+"/editors", any(nil)
	if method == "POST" {
		body = map[string]string{"person": who}
	} else if method == "DELETE" {
		path += "/" + who
	}
	if owner != "" {
		path += "?owner=" + owner
	}
	return a.At(t, method, teamAPIHost, path, body, teamKey(by))
}

// newEditor has ann make a new person an editor of her site, returning the
// editor and ann's handle.
func newEditor(t *testing.T, a *servertest.PrivateApp, ann servertest.Person, site string) (servertest.Person, string) {
	t.Helper()
	bea := a.NewPerson(t, "editor")
	_, beaHandle := a.UserID(t, bea)
	_, annHandle := a.UserID(t, ann)
	if r := editors(t, a, ann, "POST", site, "", beaHandle); r.Status != http.StatusOK || !strings.Contains(string(r.Body), beaHandle) {
		t.Fatalf("add editor: %d %s", r.Status, r.Body)
	}
	return bea, annHandle
}

func TestSiteEditorPublishesTheSiteWithOwnKey(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-owner")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.Status >= 300 || !strings.Contains(r.JSON(t)["site_url"].(string), annHandle) {
		t.Fatalf("editor creates the site: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.Status != http.StatusOK {
		t.Fatalf("editor updates the site: %d %s", r.Status, r.Body)
	}
}

func TestSiteEditorRefusedOnAnotherSite(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-other")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "blog"); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "owner_not_found" {
		t.Fatalf("editor on another site: %d %s", r.Status, r.Body)
	}
}

func TestSiteEditorHeldToEditorRoutes(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-scope")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	for _, r := range []servertest.Resp{
		a.At(t, "DELETE", teamAPIHost, "/v1/sites/shop?owner="+annHandle, nil, teamKey(bea)),
		editors(t, a, bea, "GET", "shop", annHandle, ""),
	} {
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "editor_scope" {
			t.Fatalf("editor off their routes: %d %s", r.Status, r.Body)
		}
	}
}

func TestSiteEditorsManagedByTeamMembers(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben, cal, dee := a.NewPerson(t, "editor-team"), a.NewPerson(t, "editor-team-b"), a.NewPerson(t, "editor-team-c"), a.NewPerson(t, "editor-team-d")
	team := newTeam(t, a, ann, "editors")
	addMember(t, a, ann, team, ben)
	_, deeHandle := a.UserID(t, dee)
	if r := editors(t, a, ben, "POST", "shop", team, deeHandle); r.Status != http.StatusOK {
		t.Fatalf("member adds an editor: %d %s", r.Status, r.Body)
	}
	if r := editors(t, a, ann, "GET", "shop", team, ""); r.Status != http.StatusOK || len(r.JSON(t)["editors"].([]any)) != 1 {
		t.Fatalf("member lists editors: %d %s", r.Status, r.Body)
	}
	if r := editors(t, a, cal, "POST", "shop", team, deeHandle); r.Status != http.StatusNotFound {
		t.Fatalf("non-member adds an editor: %d %s", r.Status, r.Body)
	}
	if r := editors(t, a, ben, "DELETE", "shop", team, deeHandle); r.Status != http.StatusOK || len(r.JSON(t)["editors"].([]any)) != 0 {
		t.Fatalf("member removes an editor: %d %s", r.Status, r.Body)
	}
}

func TestSiteEditorRemovedIsRefusedOnNextRequest(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-removal")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	_, beaHandle := a.UserID(t, bea)
	if r := editors(t, a, ann, "DELETE", "shop", "", beaHandle); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "owner_not_found" {
		t.Fatalf("removed editor's next request: %d %s", r.Status, r.Body)
	}
}

func TestSiteEditorFollowsARename(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-rename")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, ann, annHandle, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", teamAPIHost, "/v1/sites/shop", map[string]any{"name": "store"}, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "store"); r.Status != http.StatusOK {
		t.Fatalf("editor publishes the renamed site: %d %s", r.Status, r.Body)
	}
}

func TestSiteEditorOwnsTheSiteStorage(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "editor-store")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, ann, annHandle, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	// Rules-less resource: owner-only.
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/resources/notes?owner="+annHandle, map[string]string{"kind": "kv"}, teamKey(bea)); r.Status != http.StatusCreated {
		t.Fatalf("editor creates resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/kv/notes/keys/k?owner="+annHandle, `{"value":"editors too"}`, teamKey(bea)); r.Status != http.StatusOK {
		t.Fatalf("editor writes: %d %s", r.Status, r.Body)
	}
}
