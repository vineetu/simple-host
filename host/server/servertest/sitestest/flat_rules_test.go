package sitestest

import (
	"context"
	"errors"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/deploy"
	"github.com/vsriram/simple-host/host/sites/lifecycle"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// flat runs only where every one-level label splits into the one site it
// was built from: each kind of existing name that could break that is
// counted, and any one of them turns flat off at startup. Simple Hack runs
// it only with TEAM_SITE_FLAT_HOSTS.
func TestFlatPrecondition(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	handles := func() int {
		n, err := accountsrepo.CountDoubleHyphenHandles(ctx, a.Database)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	blockers := func() sitesrepo.FlatBlockers {
		b, err := sitesrepo.GetFlatBlockers(ctx, a.Database, rt.SiteDomain, sbBase)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	h0, b0 := handles(), blockers()
	p := a.NewPerson(t, "pre")
	a.files(t, p, "POST", "one", map[string]string{"index.html": "x"})
	a.files(t, p, "POST", "two", map[string]string{"index.html": "x"})
	a.files(t, p, "POST", "three", map[string]string{"index.html": "x"})
	a.withHandle(t, a.NewPerson(t, "cur"), fixture.Uniq("cur--"))
	exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, NULL)`, fixture.Uniq("gone--")) // retired on erasure
	exec(`UPDATE sites SET name = $2 WHERE user_id = $1 AND name = 'one'`, p.ID, "o--ne")
	exec(`UPDATE sites SET custom_domain = $2, domain_verified_at = now() WHERE user_id = $1 AND name = 'two'`, p.ID, fixture.Uniq("brand")+"."+sbBase)
	exec(`UPDATE sites SET custom_domain = $2, domain_verified_at = now() WHERE user_id = $1 AND name = 'three'`, p.ID, fixture.Uniq("c--d")+"."+rt.SiteDomain)
	exec(`UPDATE sites SET name = $2 WHERE user_id = $1 AND name = 'three'`, p.ID, strings.Repeat("t", 62-len(p.Handle)))
	if got := handles(); got != h0+2 {
		t.Fatalf("handles: %d, want %d", got, h0+2)
	}
	if b := blockers(); b.SiteNames != b0.SiteNames+1 || b.OnBase != b0.OnBase+1 || b.FreeNames != b0.FreeNames+1 || b.TooLong != b0.TooLong+1 {
		t.Fatalf("blockers: %+v, before %+v", b, b0)
	}
	a.Hosts.SetSiteBase(sbBase, "flat")
	a.Hosts.RequireFlatClean(ctx, false)
	if a.Hosts.Addressing().BaseSplit() {
		t.Fatal("flat kept on with blocking names")
	}

	for _, teamFlat := range []bool{false, true} {
		hack := sites.NewHosts(nil, nil, groupEdition(), rt.SiteDomain, rt.ContentHost, "", "")
		hack.SetSiteBase(sbBase, "flat")
		hack.RequireFlatClean(ctx, teamFlat)
		if hack.Addressing().BaseSplit() != teamFlat {
			t.Errorf("Simple Hack, TEAM_SITE_FLAT_HOSTS=%v: split %v", teamFlat, hack.Addressing().BaseSplit())
		}
	}
}

// Create and rename check <site>--<handle> again under the account lock:
// a handle changed after the request was read is the one that counts.
func TestFlatLabelRecheckedUnderLock(t *testing.T) {
	a := newBaseApp(t, "flat")
	ctx := context.Background()
	p := a.NewPerson(t, "lock")
	a.files(t, p, "POST", "keep", map[string]string{"index.html": "x"})
	stale := model.User{ID: p.ID, Username: p.Email}
	stale.Handle.String, stale.Handle.Valid = p.Handle, true
	name := strings.Repeat("n", 61-len(p.Handle)) // fits the handle as read
	a.withHandle(t, p, p.Handle+"z")              // changed meanwhile
	var siteErr *sites.Error
	_, err := deploy.Service{Service: a.Sites.SitesSvc()}.Create(ctx, deploy.CreateInput{User: &stale, Name: name, Files: map[string][]byte{"index.html": []byte("x")}})
	if !errors.As(err, &siteErr) || siteErr.Code != "invalid_name" {
		t.Fatalf("create: %v", err)
	}
	_, err = lifecycle.Service{Service: a.Sites.LifecycleSvc()}.Rename(ctx, lifecycle.RenameInput{UserID: p.ID, OldName: "keep", NewName: name, Handle: p.Handle})
	if !errors.As(err, &siteErr) || siteErr.Code != "invalid_name" {
		t.Fatalf("rename: %v", err)
	}
	if _, err := sitesrepo.GetSiteByUser(ctx, a.Database, p.ID, "keep"); err != nil {
		t.Fatalf("refused rename moved the site: %v", err)
	}
}
