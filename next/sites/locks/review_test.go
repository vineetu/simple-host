package locks_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/locks"
)

// Review A6: a gate form's next and a visitor sign-in's return only ever
// lead to a path on the same host.
func TestGoBackStaysOnTheHost(t *testing.T) {
	k := testkit.Start(t, features)
	v := k.Person(t, "victim")
	s := site(t, k, v, "blog")
	if err := locks.SetPasscode(context.Background(), k.App, s, "secret123"); err != nil {
		t.Fatal(err)
	}
	bad := []string{"/\t/evil.com", "/\\evil.com", "/\n/evil.com", "//evil.com", "/ /evil.com", "https://evil.com"}
	for i, n := range bad {
		r := testkit.Call(t, k.Browser(), "POST", "https://blog--victim.simple-host.site/_sh/passcode",
			"passcode=secret123&next="+url.QueryEscape(n), "Content-Type", form, "X-Real-IP", fmt.Sprintf("10.9.0.%d", i))
		if r.Status != 303 || r.Header.Get("Location") != "/" {
			t.Fatalf("next=%q: %d Location %q", n, r.Status, r.Header.Get("Location"))
		}
		if r := testkit.Call(t, k.Browser(), "GET", "https://blog--victim.simple-host.site/_sh/signin?return="+url.QueryEscape(n), nil); r.Status != 400 {
			t.Fatalf("return=%q: %d %s", n, r.Status, r.Header.Get("Location"))
		}
	}
}
