package shortnames_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/core/web"
	"simplehost/sites/serve"
	"simplehost/sites/shortnames"
)

// SPEC test 17 (F12): a free short name works at once. A taken one is refused.
func TestShortName(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	olga := k.Person(t, "olga")
	k.Publish(t, bob, "blog", map[string]string{"index.html": "bob's blog"})
	k.Publish(t, olga, "shop", map[string]string{"index.html": "olga's shop"})
	ctx := context.Background()
	blog, _ := k.App.Sites.Get(ctx, bob.ID, "blog")
	shop, _ := k.App.Sites.Get(ctx, olga.ID, "shop")
	asBob := web.WithCaller(httptest.NewRequest("PATCH", "/", nil), web.Caller{ID: bob.ID})
	asOlga := web.WithCaller(httptest.NewRequest("PATCH", "/", nil), web.Caller{ID: olga.ID})

	if r := testkit.Call(t, b, "GET", "https://cats.simple-host.app/", nil); r.Status != 404 {
		t.Fatalf("unclaimed name: %d", r.Status)
	}
	url, err := shortnames.Claim(asBob, k.App, blog, "cats")
	if err != nil || url != "https://cats.simple-host.app/" {
		t.Fatalf("claim: %q %v", url, err)
	}
	if r := testkit.Call(t, b, "GET", url, nil); r.Status != 200 || r.Body != "bob's blog" {
		t.Fatalf("short name: %d %q", r.Status, r.Body)
	}
	for _, name := range []string{"cats", "bob"} { // another site's short name, a handle
		if _, err := shortnames.Claim(asOlga, k.App, shop, name); err == nil || !strings.Contains(err.Error(), "name_taken") {
			t.Fatalf("taken name %s: %v", name, err)
		}
	}
	if _, err := shortnames.Claim(asOlga, k.App, blog, "dogs"); err == nil {
		t.Fatal("olga named bob's site")
	}
	// Let go: it stops reaching the site, and nobody can take it again.
	if err := shortnames.Release(asBob, k.App, blog); err != nil {
		t.Fatal(err)
	}
	if r := testkit.Call(t, b, "GET", url, nil); r.Status != 404 {
		t.Fatalf("released name: %d", r.Status)
	}
	if _, err := shortnames.Claim(asOlga, k.App, shop, "cats"); err == nil {
		t.Fatal("a released name was taken again")
	}
}
