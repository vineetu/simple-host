package lifecycle_test

import (
	"testing"
	"time"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/lifecycle"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// app is the fixture with the lifecycle routes mounted.
func app(t *testing.T) *fixture.App {
	t.Helper()
	a := fixture.New(t)
	lifecycle.Register(a.Mux, a.H, a.Auth, a.Notice, a.SiteOps)
	return a
}

// C4: the per-site lock is per account, and a delete or restore of a name
// the account does not have takes no lock at all.
func TestSiteLocksPerAccount(t *testing.T) {
	a := app(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop")
	unlock := a.H.LockSite(olive.ID, "shop")
	done := make(chan rt.Resp, 1)
	go func() { done <- a.Files(t, oscar, "PUT", "shop", map[string]string{"index.html": "o2"}) }()
	select {
	case r := <-done:
		if r.Status != 200 {
			t.Fatalf("other account's deploy: %d %s", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("another account's site of the same name waited on this lock")
	}
	unlock()
	for _, c := range []struct{ method, path string }{{"DELETE", "/v1/sites/nope"}, {"POST", "/v1/sites/nope/restore"}} {
		if r := a.API(t, oscar, c.method, c.path, nil); r.Status != 404 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if _, ok := a.H.UploadLocks.Load(oscar.ID + "/nope"); ok {
		t.Fatal("a lock was made for a name the account does not have")
	}
}
