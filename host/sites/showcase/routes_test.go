package showcase_test

import (
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/showcase"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// The feed is rate limited per reader across hosts; an unknown handle is 404.
func TestShowcaseFeedReadLimit(t *testing.T) {
	a := fixture.New(t)
	showcase.Register(a.Mux, a.H, a.Auth, a.Notice)
	owner := a.NewPerson(t, "feedlimit")
	a.H.ShowcaseLimiter = httpx.NewRateLimiter(1, 0)
	path := "/v1/u/" + owner.Handle + "/showcase.json"
	if r := a.At(t, "GET", rt.SiteDomain, path, nil, nil); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	if r := a.At(t, "GET", owner.Handle+"."+rt.SiteDomain, path, nil, nil); r.Status != 429 {
		t.Fatalf("rate across hosts: %d", r.Status)
	}
	a.H.ShowcaseLimiter = httpx.NewRateLimiter(100, 0)
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/u/not-a-person/showcase.json", nil, nil); r.Status != 404 {
		t.Fatalf("missing: %d", r.Status)
	}
}
