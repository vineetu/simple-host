package legacy

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

func TestCollectionPrivateReadAllowed(t *testing.T) {
	for _, tc := range []struct {
		owner, shared, originless, allowed bool
	}{
		{true, false, true, true},
		{true, true, false, false},
		{false, false, true, false},
		{false, true, false, true},
	} {
		if got := CollectionPrivateReadAllowed(tc.owner, tc.shared, tc.originless); got != tc.allowed {
			t.Errorf("owner=%v shared=%v originless=%v: got %v", tc.owner, tc.shared, tc.originless, got)
		}
	}
}

func TestCollectionListSiteGateAndScope(t *testing.T) {
	gateCalls, resolveCalls := 0, 0
	allow := false
	a := New(nil, time.Time{}, Deps{
		AuthorizeCollectionOrigin: func(string) bool { gateCalls++; return allow },
		ResolveNamedSite:          func(context.Context, string) (string, error) { resolveCalls++; return "site-id", nil },
	})
	in := CollectionListSiteInput{OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items"}
	for _, tc := range []struct {
		in   CollectionListSiteInput
		code string
	}{
		{CollectionListSiteInput{}, "site_name_required"},
		{CollectionListSiteInput{OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "bad/name"}, "invalid_collection"},
	} {
		_, _, err := a.collectionListSite(context.Background(), tc.in)
		var opErr *Error
		if !errors.As(err, &opErr) || opErr.Code != tc.code || gateCalls != 0 || resolveCalls != 0 {
			t.Fatalf("validation: err=%v gates=%d resolves=%d", err, gateCalls, resolveCalls)
		}
	}
	_, _, err := a.collectionListSite(context.Background(), in)
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "origin_not_allowed" || resolveCalls != 0 {
		t.Fatalf("gate failure: err=%v resolves=%d", err, resolveCalls)
	}
	in.Originless = true
	in.Scoped, in.ScopedSiteID = true, "other"
	_, _, err = a.collectionListSite(context.Background(), in)
	if !errors.Is(err, sql.ErrNoRows) || gateCalls != 1 || resolveCalls != 1 {
		t.Fatalf("scope failure: err=%v gates=%d resolves=%d", err, gateCalls, resolveCalls)
	}
}

func TestListSiteCollectionsAuthorization(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		name string
		in   ListSiteCollectionsInput
		code string
	}{
		{"anonymous before missing name", ListSiteCollectionsInput{}, "unauthorized"},
		{"anonymous with name and admin flag", ListSiteCollectionsInput{SiteName: "site", Admin: true}, "unauthorized"},
		{"missing name", ListSiteCollectionsInput{SignedIn: true}, "site_name_required"},
		{"whitespace name", ListSiteCollectionsInput{SignedIn: true, SiteName: " \t "}, "site_name_required"},
		{"missing name before scoped lookup", ListSiteCollectionsInput{SignedIn: true, UserID: "user", Scoped: true, ScopedSiteID: "site"}, "site_name_required"},
		{"signed-in caller without an identity", ListSiteCollectionsInput{SiteName: "site", SignedIn: true}, "site_not_found"},
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
		{CollectionGateInput{KeyWithoutPage: true}, "invalid_site_or_collection"},
		{CollectionGateInput{SiteName: "site", Collection: "bad name", KeyWithoutPage: true}, "invalid_site_or_collection"},
		{CollectionGateInput{SiteName: "site", Collection: "list", KeyWithoutPage: true}, ""},
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

func TestAuthorizeCollectionSubmitter(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		kind, actor string
		refused     bool
	}{
		{db.KindEntries, "anonymous", true},
		{db.KindEntries, "visitor", false},
		{"", "anonymous", false},
	} {
		err := a.authorizeCollectionSubmitter(context.Background(), db.DataSettings{Kind: tc.kind}, db.Actor{Kind: tc.actor})
		if (err != nil) != tc.refused {
			t.Fatalf("kind %q actor %q: error = %v", tc.kind, tc.actor, err)
		}
		if tc.refused {
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != "visitor_auth_required" {
				t.Fatalf("kind %q actor %q: unexpected error %v", tc.kind, tc.actor, err)
			}
		}
	}
}

func TestCollectionBodyValidationOrder(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{CollectionJSONStorable: func([]byte) bool {
		calls++
		return false
	}})
	for _, body := range []string{"", "{"} {
		var opErr *Error
		if err := a.validateCollectionBody([]byte(body)); !errors.As(err, &opErr) || opErr.Code != "invalid_collection_body" || calls != 0 {
			t.Fatalf("body %q: error = %v, storable checks = %d", body, err, calls)
		}
	}
	var opErr *Error
	if err := a.validateCollectionBody([]byte(`"\u0000"`)); !errors.As(err, &opErr) || opErr.Code != "invalid_json" || calls != 1 {
		t.Fatalf("unstorable body: error = %v, storable checks = %d", err, calls)
	}
}

func TestCollectionGateOriginOrder(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{AuthorizeCollectionOrigin: func(name string) bool {
		calls++
		return name == "site"
	}})
	_ = a.AuthorizeCollectionGate(context.Background(), CollectionGateInput{SiteName: "site", Collection: "bad name"})
	_ = a.AuthorizeCollectionGate(context.Background(), CollectionGateInput{SiteName: "site", Collection: "list", KeyWithoutPage: true})
	if calls != 0 {
		t.Fatal("origin authorization ran before validation or for a key-only request")
	}
	if err := a.AuthorizeCollectionGate(context.Background(), CollectionGateInput{SiteName: "site", Collection: "list"}); err != nil || calls != 1 {
		t.Fatalf("origin authorization: calls = %d, error = %v", calls, err)
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

func TestReadCollectionStopsBeforeReadCheckOnInvalidTarget(t *testing.T) {
	called := false
	a := New(nil, time.Time{}, Deps{CollectionReadAllowed: func(string, bool) bool {
		called = true
		return true
	}})
	_, err := a.ReadCollection(context.Background(), CollectionPageRequest{})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "site_name_required" || called {
		t.Fatalf("error = %v, read check called = %v", err, called)
	}
}
