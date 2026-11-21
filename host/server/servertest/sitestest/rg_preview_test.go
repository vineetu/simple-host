package sitestest

import (
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// A server that gives sites no address of their own (every site on one shared
// origin) makes no preview link: the stored version is held but unshown, and
// the mint route refuses with preview_unavailable and says why.
func TestNoPreviewOnSharedOrigin(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetPersonHosts("off")
	a.Hosts.SetSiteHosts("off", "")
	if !a.Sites.SharedOrigin() {
		t.Fatal("the app still gives sites their own addresses")
	}
	ann := a.NewPerson(t, "ann")
	a.Deploy(t, ann, "blog")
	r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/blog/files?publish=false", map[string]any{"files": map[string]string{"index.html": "v2"}}, ann.Header())
	if r.Status != 200 || r.JSON(t)["preview_url"] != nil {
		t.Fatalf("publish=false: %d %s", r.Status, r.Body)
	}
	p := a.At(t, "POST", rt.SiteDomain, "/v1/sites/blog/versions/2/preview-link", nil, ann.Header())
	if p.Status != 409 || p.JSON(t)["code"] != "preview_unavailable" {
		t.Fatalf("preview link: %d %s", p.Status, p.Body)
	}
}
