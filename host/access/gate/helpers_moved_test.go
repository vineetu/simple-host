package gate

import (
	"net/http"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// teamNewEditor has ann make a new person an editor of her site.
func teamNewEditor(t *testing.T, a *App, ann Person, site string) Person {
	t.Helper()
	bea := a.NewPerson(t, "editor")
	if r := teamEditors(t, a, ann, "POST", site, "", bea.Handle); r.Status != http.StatusOK || !strings.Contains(string(r.Body), bea.Handle) {
		t.Fatalf("add editor: %d %s", r.Status, r.Body)
	}
	return bea
}

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
