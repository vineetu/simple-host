package handler

import (
	"net/http"
	"strings"
	"testing"
)

// Site editors (TM-S2): a person who publishes one site of an account with
// their own key and owner=<account>, without belonging to it.

// editors calls the editors routes of site as by, acting for owner when set.
func editors(t *testing.T, a *privateApp, by person, method, site, owner, who string) resp {
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
	return a.at(t, method, teamAPIHost, path, body, teamKey(by))
}

// newEditor has ann make a new person an editor of her site, returning the
// editor and ann's handle.
func newEditor(t *testing.T, a *privateApp, ann person, site string) (person, string) {
	t.Helper()
	bea := a.newPerson(t, "editor")
	_, beaHandle := a.userID(t, bea)
	_, annHandle := a.userID(t, ann)
	if r := editors(t, a, ann, "POST", site, "", beaHandle); r.status != http.StatusOK || !strings.Contains(string(r.body), beaHandle) {
		t.Fatalf("add editor: %d %s", r.status, r.body)
	}
	return bea, annHandle
}

func TestSiteEditorPublishesTheSiteWithOwnKey(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-owner")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.status >= 300 || !strings.Contains(r.json(t)["site_url"].(string), annHandle) {
		t.Fatalf("editor creates the site: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.status != http.StatusOK {
		t.Fatalf("editor updates the site: %d %s", r.status, r.body)
	}
}

func TestSiteEditorRefusedOnAnotherSite(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-other")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "blog"); r.status != http.StatusNotFound || r.json(t)["code"] != "owner_not_found" {
		t.Fatalf("editor on another site: %d %s", r.status, r.body)
	}
}

func TestSiteEditorHeldToEditorRoutes(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-scope")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	for _, r := range []resp{
		a.at(t, "DELETE", teamAPIHost, "/v1/sites/shop?owner="+annHandle, nil, teamKey(bea)),
		editors(t, a, bea, "GET", "shop", annHandle, ""),
	} {
		if r.status != http.StatusForbidden || r.json(t)["code"] != "editor_scope" {
			t.Fatalf("editor off their routes: %d %s", r.status, r.body)
		}
	}
}

func TestSiteEditorsManagedByTeamMembers(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben, cal, dee := a.newPerson(t, "editor-team"), a.newPerson(t, "editor-team-b"), a.newPerson(t, "editor-team-c"), a.newPerson(t, "editor-team-d")
	team := newTeam(t, a, ann, "editors")
	addMember(t, a, ann, team, ben)
	_, deeHandle := a.userID(t, dee)
	if r := editors(t, a, ben, "POST", "shop", team, deeHandle); r.status != http.StatusOK {
		t.Fatalf("member adds an editor: %d %s", r.status, r.body)
	}
	if r := editors(t, a, ann, "GET", "shop", team, ""); r.status != http.StatusOK || len(r.json(t)["editors"].([]any)) != 1 {
		t.Fatalf("member lists editors: %d %s", r.status, r.body)
	}
	if r := editors(t, a, cal, "POST", "shop", team, deeHandle); r.status != http.StatusNotFound {
		t.Fatalf("non-member adds an editor: %d %s", r.status, r.body)
	}
	if r := editors(t, a, ben, "DELETE", "shop", team, deeHandle); r.status != http.StatusOK || len(r.json(t)["editors"].([]any)) != 0 {
		t.Fatalf("member removes an editor: %d %s", r.status, r.body)
	}
}

func TestSiteEditorRemovedIsRefusedOnNextRequest(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-removal")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	_, beaHandle := a.userID(t, bea)
	if r := editors(t, a, ann, "DELETE", "shop", "", beaHandle); r.status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "shop"); r.status != http.StatusNotFound || r.json(t)["code"] != "owner_not_found" {
		t.Fatalf("removed editor's next request: %d %s", r.status, r.body)
	}
}

func TestSiteEditorFollowsARename(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-rename")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, ann, annHandle, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", teamAPIHost, "/v1/sites/shop", map[string]any{"name": "store"}, teamKey(ann)); r.status != http.StatusOK {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, bea, annHandle, "store"); r.status != http.StatusOK {
		t.Fatalf("editor publishes the renamed site: %d %s", r.status, r.body)
	}
}

func TestSiteEditorOwnsTheSiteStorage(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "editor-store")
	bea, annHandle := newEditor(t, a, ann, "shop")
	if r := teamDeploy(t, a, ann, annHandle, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	// Rules-less resource: owner-only.
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/resources/notes?owner="+annHandle, map[string]string{"kind": "kv"}, teamKey(bea)); r.status != http.StatusCreated {
		t.Fatalf("editor creates resource: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/kv/notes/keys/k?owner="+annHandle, `{"value":"editors too"}`, teamKey(bea)); r.status != http.StatusOK {
		t.Fatalf("editor writes: %d %s", r.status, r.body)
	}
}
