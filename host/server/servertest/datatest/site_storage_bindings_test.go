package datatest

import (
	"context"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

type ownerBindingFixture struct {
	appstorage.NoBindings
	calls int
}

func (p *ownerBindingFixture) OwnerOnSite(context.Context, string, string) (bool, error) {
	p.calls++
	return true, nil
}

// The owner signed in on the site's own address has owner rights (with the
// platform's default provider, also when none is installed); a visitor, or
// the owner framed in another page, does not.
func TestStorageOwnerOnSite(t *testing.T) {
	s := newShop(t)
	s.A.Data.SetStorageBindings(nil)
	s.Must(t, "PUT", "/resources/private", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/private/keys/key", `{"value":"only owner"}`, s.Key, 200)
	s.Must(t, "GET", "/kv/private/keys/key", nil, s.Callers["owner-site"], 200)
	s.Must(t, "GET", "/kv/private/keys/key", nil, s.Callers["alice"], 403)
	framed := rt.Browser(s.Host, "", "Cookie", s.Callers["owner-site"]["Cookie"], "X-SH-Framed", "1")
	s.Must(t, "GET", "/kv/private/keys/key", nil, framed, 403)

	// An installed provider decides who acts as the owner on the site.
	p := &ownerBindingFixture{}
	s.A.Data.SetStorageBindings(p)
	s.Must(t, "GET", "/kv/private/keys/key", nil, s.Callers["alice"], 200)
	s.Must(t, "GET", "/kv/private/keys/key", nil, s.Callers["anon"], 403)
	if p.calls == 0 {
		t.Fatal("provider not consulted")
	}
}
