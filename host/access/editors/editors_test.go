package editors

import (
	"net/http"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Site editors (TM-S2): a person who publishes one site of an account with
// their own key and owner=<account>, without belonging to it. What an editor
// may publish is Sites' use of this resolution; here the editors routes and
// the owner resolution are proven through Access routes.

// teamEditors calls the editors routes of site as by, acting for owner when set.
func teamEditors(t *testing.T, a *App, by Person, method, site, owner, who string) Resp {
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
	return a.At(t, method, rt.SiteDomain, path, body, TeamKey(by))
}

// teamNewEditor has ann make a new person an editor of her site.
func teamNewEditor(t *testing.T, a *App, ann Person, site string) Person {
	t.Helper()
	bea := a.NewPerson(t, "editor")
	if r := teamEditors(t, a, ann, "POST", site, "", bea.Handle); r.Status != http.StatusOK || !strings.Contains(string(r.Body), bea.Handle) {
		t.Fatalf("add editor: %d %s", r.Status, r.Body)
	}
	return bea
}

// An editor is resolved as the account on their one site only, and held to
// the editor routes there; on another site they are refused as a stranger,
// and removal takes effect on the next request.
func TestSiteEditorIsResolvedOnTheirSiteOnly(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "editor-owner")
	a.Deploy(t, ann, "shop")
	a.Deploy(t, ann, "blog")
	bea := teamNewEditor(t, a, ann, "shop")
	for _, r := range []Resp{
		TeamActs(t, a.Base, bea, ann.Handle, "shop"),
		teamEditors(t, a, bea, "GET", "shop", ann.Handle, ""),
	} {
		if r.Status != http.StatusForbidden || r.Code() != "editor_scope" {
			t.Fatalf("editor off their routes: %d %s", r.Status, r.Body)
		}
	}
	if r := TeamActs(t, a.Base, bea, ann.Handle, "blog"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("editor on another site: %d %s", r.Status, r.Body)
	}
	if r := teamEditors(t, a, ann, "DELETE", "shop", "", bea.Handle); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := TeamActs(t, a.Base, bea, ann.Handle, "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("removed editor's next request: %d %s", r.Status, r.Body)
	}
}
