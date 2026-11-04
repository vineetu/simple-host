package legacy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListSiteCollectionsAuthorization(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		name string
		in   OwnedSiteInput
		code string
	}{
		{"anonymous before missing name", OwnedSiteInput{}, "unauthorized"},
		{"anonymous with name and admin flag", OwnedSiteInput{SiteName: "site", Admin: true}, "unauthorized"},
		{"missing name", OwnedSiteInput{SignedIn: true}, "site_name_required"},
		{"whitespace name", OwnedSiteInput{SignedIn: true, SiteName: " \t "}, "site_name_required"},
		{"missing name before scoped lookup", OwnedSiteInput{SignedIn: true, UserID: "user", Scoped: true, ScopedSiteID: "site"}, "site_name_required"},
		{"signed-in caller without an identity", OwnedSiteInput{SiteName: "site", SignedIn: true}, "site_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.ListSiteCollections(context.Background(), tc.in)
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.code {
				t.Fatalf("got %v, want code %q", err, tc.code)
			}
		})
	}
}

func TestOwnerSiteFromKey(t *testing.T) {
	called := false
	a := New(nil, time.Time{}, Deps{
		AdminAPIKey: "admin-key",
		ResolveNamedSite: func(_ context.Context, name string) (string, error) {
			called = true
			if name != "site" {
				t.Fatalf("site name = %q", name)
			}
			return "site-id", nil
		},
	})
	if _, ok := a.OwnerSiteFromKey(context.Background(), OwnerKeyInput{APIKey: "admin-key"}); ok || called {
		t.Fatal("missing site should not resolve")
	}
	if _, ok := a.OwnerSiteFromKey(context.Background(), OwnerKeyInput{SiteName: "site"}); ok || called {
		t.Fatal("missing key should not resolve")
	}
	id, ok := a.OwnerSiteFromKey(context.Background(), OwnerKeyInput{SiteName: "site", APIKey: "admin-key", Handle: "owner"})
	if !ok || id != "site-id" || !called {
		t.Fatalf("got (%q, %v), resolver called = %v", id, ok, called)
	}
}

func TestCollectionGate(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		in   CollectionGateInput
		code string
	}{
		{CollectionGateInput{}, "site_name_required"},
		{CollectionGateInput{SiteName: "site", Collection: "bad name"}, "invalid_collection"},
		{CollectionGateInput{SiteName: "site", Collection: "list"}, "origin_not_allowed"},
		{CollectionGateInput{SiteName: "site", Collection: "list", OriginAllowed: true}, ""},
	} {
		err := a.AuthorizeCollectionGate(context.Background(), tc.in)
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code {
			t.Fatalf("got %v, want %s", err, tc.code)
		}
	}
}

func TestCollectionOptions(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		in      CollectionOptionsInput
		allowed bool
	}{
		{CollectionOptionsInput{SiteName: " \t ", OriginAllowed: true}, false},
		{CollectionOptionsInput{SiteName: " site ", OriginAllowed: false}, false},
		{CollectionOptionsInput{SiteName: " site ", OriginAllowed: true}, true},
	} {
		err := a.CollectionOptions(context.Background(), tc.in)
		if (err == nil) != tc.allowed {
			t.Fatalf("CollectionOptions(%+v) = %v", tc.in, err)
		}
	}
}
