package siteanalytics_test

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/siteanalytics"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// ?owner=<handle> on the per-site analytics endpoints: the platform admin may
// read any account's site; everyone else gets exactly the 404 of a site that
// does not exist.
func TestSiteAnalyticsOwnerParam(t *testing.T) {
	a := fixture.New(t)
	siteanalytics.Register(a.Mux, a.H, a.Auth, a.Notice)
	olive, oscar, boss := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar"), a.NewPerson(t, "boss")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "oscar-only")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, boss.ID); err != nil {
		t.Fatal(err)
	}
	want := func(label, key string, code int, site, owner string) []rt.Resp {
		t.Helper()
		var out []rt.Resp
		q := "?days=7"
		if owner != "" {
			q += "&owner=" + url.QueryEscape(owner)
		}
		for _, p := range []string{"/analytics", "/analytics/geo", "/analytics/top"} {
			h := map[string]string{}
			if key != "" {
				h["X-API-Key"] = key
			}
			r := a.At(t, "GET", rt.SiteDomain, "/v1/sites/"+site+p+q, nil, h)
			if r.Status != code {
				t.Errorf("%s: GET %s = %d, want %d (%s)", label, p, r.Status, code, r.Body)
			}
			out = append(out, r)
		}
		return out
	}
	want("no key", "", http.StatusUnauthorized, "shop", olive.Handle)
	want("own site + own handle, other case", olive.Key, http.StatusOK, "shop", strings.ToUpper(olive.Handle))
	missing := want("missing site", olive.Key, http.StatusNotFound, "no-such-site", "")
	got := want("non-admin naming another owner", olive.Key, http.StatusNotFound, "oscar-only", oscar.Handle)
	for i := range got {
		if !bytes.Equal(got[i].Body, missing[i].Body) {
			t.Errorf("another owner's site is distinguishable from a missing one: %s vs %s", got[i].Body, missing[i].Body)
		}
	}
	for _, adm := range []struct{ label, key string }{{"admin key", a.Admin}, {"is_admin account", boss.Key}} {
		want(adm.label+" reads other owner", adm.key, http.StatusOK, "oscar-only", oscar.Handle)
		want(adm.label+" wrong owner for site", adm.key, http.StatusNotFound, "oscar-only", olive.Handle)
		want(adm.label+" no owner", adm.key, http.StatusNotFound, "oscar-only", "")
	}
}
