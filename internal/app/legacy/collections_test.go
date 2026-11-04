package legacy

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

func TestCollectionReadTargetWithoutKeyDoesNotResolveSite(t *testing.T) {
	called := false
	a := New(nil, time.Time{}, Deps{
		ResolveNamedSite: func(context.Context, string) (string, error) {
			called = true
			return "site-id", nil
		},
	})
	id, owner, err := a.collectionReadTarget(context.Background(), CollectionListSiteInput{
		OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items",
	})
	if err != nil || owner || id != "" || called {
		t.Fatalf("id=%q owner=%v err=%v resolved=%v", id, owner, err, called)
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
		{"entries", "anonymous", true},
		{"entries", "visitor", false},
		{"", "anonymous", false},
	} {
		err := a.authorizeCollectionSubmitter(context.Background(), model.DataSettings{Kind: tc.kind}, model.Actor{Kind: tc.actor})
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

func TestCollectionGateOriginOrder(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		in   CollectionGateInput
		code string
	}{
		{CollectionGateInput{SiteName: "site", Collection: "bad name"}, "invalid_collection"},
		{CollectionGateInput{SiteName: "site", Collection: "list"}, "origin_not_allowed"},
		{CollectionGateInput{SiteName: "site", Collection: "list", KeyWithoutPage: true}, ""},
		{CollectionGateInput{SiteName: "site", Collection: "list", OriginAllowed: true}, ""},
	} {
		err := a.AuthorizeCollectionGate(context.Background(), tc.in)
		if tc.code == "" {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			continue
		}
		var opErr *Error
		if !errors.As(err, &opErr) || opErr.Code != tc.code {
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

func TestCollectionReadTargetValidatesBeforeKeyLookup(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, tc := range []struct {
		in   CollectionListSiteInput
		code string
	}{
		{CollectionListSiteInput{}, "site_name_required"},
		{CollectionListSiteInput{OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "bad/name"}, "invalid_collection"},
	} {
		_, _, err := a.collectionReadTarget(context.Background(), tc.in)
		var opErr *Error
		if !errors.As(err, &opErr) || opErr.Code != tc.code {
			t.Fatalf("got %v, want %s", err, tc.code)
		}
	}
}

func TestCollectionReadTargetResolvesOriginlessSite(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{ResolveNamedSite: func(_ context.Context, name string) (string, error) {
		calls++
		if name != "site" {
			t.Fatalf("site name = %q", name)
		}
		return "site-id", nil
	}})
	id, owner, err := a.collectionReadTarget(context.Background(), CollectionListSiteInput{
		OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items", Originless: true,
	})
	if err != nil || owner || id != "site-id" || calls != 1 {
		t.Fatalf("id=%q owner=%v err=%v calls=%d", id, owner, err, calls)
	}
}

func TestCollectionReadTargetResolutionErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"missing site", sql.ErrNoRows, "site_not_found"},
		{"lookup failure", errors.New("lookup failure"), "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(nil, time.Time{}, Deps{ResolveNamedSite: func(context.Context, string) (string, error) {
				return "", tc.err
			}})
			_, _, err := a.collectionReadTarget(context.Background(), CollectionListSiteInput{
				OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items", Originless: true,
			})
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.code {
				t.Fatalf("got %v, want %q", err, tc.code)
			}
		})
	}
}

func TestPrepareCollectionAppendStopsBeforeSiteLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   CollectionGateInput
		code string
	}{
		{"missing site", CollectionGateInput{Collection: "items"}, "site_name_required"},
		{"invalid name", CollectionGateInput{SiteName: "site", Collection: "bad/name"}, "invalid_collection"},
		{"key without page", CollectionGateInput{SiteName: "site", Collection: "bad/name", KeyWithoutPage: true}, "invalid_site_or_collection"},
		{"origin refused", CollectionGateInput{SiteName: "site", Collection: "items"}, "origin_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			a := New(nil, time.Time{}, Deps{ResolveWriteSite: func(context.Context, string) (string, error) {
				called = true
				return "site-id", nil
			}})
			_, _, err := a.prepareCollectionAppend(context.Background(), tc.in)
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.code || called {
				t.Fatalf("got err=%v lookup=%v, want code %q without lookup", err, called, tc.code)
			}
		})
	}
}

func TestAuthorizePrivateCollectionReadLookupErrors(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{CollectionOwnerDomain: func(context.Context, string) (string, bool, error) {
		calls++
		return "", false, sql.ErrNoRows
	}})
	for _, tc := range []struct {
		name  string
		in    CollectionReadViewInput
		code  string
		calls int
	}{
		{"owner on shared host", CollectionReadViewInput{OwnerKey: true, SharedHost: true}, "private_not_found", 0},
		{"originless visitor", CollectionReadViewInput{Originless: true}, "private_not_found", 0},
		{"owner key", CollectionReadViewInput{OwnerKey: true}, "", 0},
		{"missing domain lookup", CollectionReadViewInput{}, "internal", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			tc.in.Private = true
			_, err := a.collectionReadView(context.Background(), tc.in)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				var opErr *Error
				if !errors.As(err, &opErr) || opErr.Code != tc.code {
					t.Fatalf("got %v, want %q", err, tc.code)
				}
			}
			if calls != tc.calls {
				t.Fatalf("lookup calls = %d, want %d", calls, tc.calls)
			}
		})
	}
}

func TestCollectionReadViewAuthorizationOrder(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{
		CollectionBrowserSession: func(context.Context, string) CollectionBrowserSession {
			calls++
			return CollectionBrowserSession{UserID: "owner", OK: true}
		},
		CollectionOwnerDomain: func(context.Context, string) (string, bool, error) {
			return "owner", true, nil
		},
	})
	in := CollectionReadViewInput{Private: true, Originless: true, HasStrictCookie: true}
	_, err := a.collectionReadView(context.Background(), in)
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "private_not_found" || calls != 0 {
		t.Fatalf("private read: err=%v author lookups=%d", err, calls)
	}
	in.Private = false
	withAuthor, err := a.collectionReadView(context.Background(), in)
	if err != nil || !withAuthor || calls != 1 {
		t.Fatalf("public read: withAuthor=%v err=%v author lookups=%d", withAuthor, err, calls)
	}
	in.OwnerKey = true
	withAuthor, err = a.collectionReadView(context.Background(), in)
	if err != nil || !withAuthor || calls != 1 {
		t.Fatalf("owner read: withAuthor=%v err=%v author lookups=%d", withAuthor, err, calls)
	}
}

func TestCollectionReadTargetEnforcesResolvedScope(t *testing.T) {
	a := New(nil, time.Time{}, Deps{ResolveNamedSite: func(context.Context, string) (string, error) {
		return "site-id", nil
	}})
	in := CollectionListSiteInput{
		OwnerKeyInput: OwnerKeyInput{SiteName: "site", Scoped: true, ScopedSiteID: "other"},
		Collection:    "items", Originless: true,
	}
	_, _, err := a.collectionReadTarget(context.Background(), in)
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "site_not_found" {
		t.Fatalf("wrong scope: %v", err)
	}
	in.ScopedSiteID = "site-id"
	id, owner, err := a.collectionReadTarget(context.Background(), in)
	if err != nil || owner || id != "site-id" {
		t.Fatalf("matching scope: id=%q owner=%v err=%v", id, owner, err)
	}
}

func TestCollectionReadViewChecksSessionBeforeAuthorDomain(t *testing.T) {
	var order []string
	a := New(nil, time.Time{}, Deps{
		CollectionBrowserSession: func(context.Context, string) CollectionBrowserSession {
			order = append(order, "session")
			return CollectionBrowserSession{UserID: "owner", OK: true}
		},
		CollectionOwnerDomain: func(context.Context, string) (string, bool, error) {
			order = append(order, "domain")
			return "owner", true, nil
		},
	})
	in := CollectionReadViewInput{SiteID: "site", HasStrictCookie: true}
	withAuthor, err := a.collectionReadView(context.Background(), in)
	if err != nil || !withAuthor || len(order) != 2 || order[0] != "session" || order[1] != "domain" {
		t.Fatalf("withAuthor=%v err=%v order=%v", withAuthor, err, order)
	}
	order = nil
	in.VisitorPass = true
	withAuthor, err = a.collectionReadView(context.Background(), in)
	if err != nil || withAuthor || len(order) != 0 {
		t.Fatalf("visitor pass: withAuthor=%v err=%v order=%v", withAuthor, err, order)
	}
}

func TestResolveOriginCollectionChecksGateBeforeSite(t *testing.T) {
	calls := 0
	a := New(nil, time.Time{}, Deps{ResolveNamedSite: func(context.Context, string) (string, error) {
		calls++
		return "site-id", nil
	}})
	in := CollectionListSiteInput{OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items"}
	_, err := a.resolveOriginCollection(context.Background(), in)
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "origin_not_allowed" || calls != 0 {
		t.Fatalf("refused origin: err=%v site lookups=%d", err, calls)
	}
	in.OriginAllowed, in.Scoped, in.ScopedSiteID = true, true, "other"
	_, err = a.resolveOriginCollection(context.Background(), in)
	if !errors.As(err, &opErr) || opErr.Code != "site_not_found" || calls != 1 {
		t.Fatalf("wrong scope: err=%v site lookups=%d", err, calls)
	}
	in.ScopedSiteID = "site-id"
	id, err := a.resolveOriginCollection(context.Background(), in)
	if err != nil || id != "site-id" || calls != 2 {
		t.Fatalf("matched scope: id=%q err=%v site lookups=%d", id, err, calls)
	}
}

func TestCollectionReadViewSkipsAuthorLookupWithoutOwnerContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   CollectionReadViewInput
	}{
		{"no strict cookie", CollectionReadViewInput{SiteID: "site"}},
		{"shared host", CollectionReadViewInput{SiteID: "site", HasStrictCookie: true, SharedHost: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := New(nil, time.Time{}, Deps{
				CollectionBrowserSession: func(context.Context, string) CollectionBrowserSession {
					calls++
					return CollectionBrowserSession{UserID: "owner", OK: true}
				},
				CollectionOwnerDomain: func(context.Context, string) (string, bool, error) {
					calls++
					return "owner", true, nil
				},
			})
			withAuthor, err := a.collectionReadView(context.Background(), tc.in)
			if err != nil || withAuthor || calls != 0 {
				t.Fatalf("withAuthor=%v err=%v lookups=%d", withAuthor, err, calls)
			}
		})
	}
}

func TestCollectionReadViewRefusesPrivateVisitorBeforeAuthorView(t *testing.T) {
	for _, tc := range []struct {
		name        string
		userID      string
		visitorPass bool
	}{
		{"different visitor", "visitor", false},
		{"visitor pass", "owner", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			a := New(nil, time.Time{}, Deps{
				CollectionOwnerDomain: func(context.Context, string) (string, bool, error) {
					order = append(order, "domain")
					return "owner", true, nil
				},
				CollectionBrowserSession: func(context.Context, string) CollectionBrowserSession {
					order = append(order, "session")
					return CollectionBrowserSession{UserID: tc.userID, OK: true}
				},
			})
			_, err := a.collectionReadView(context.Background(), CollectionReadViewInput{
				SiteID: "site", Private: true, HasStrictCookie: true, VisitorPass: tc.visitorPass,
			})
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != "private_not_found" || len(order) != 2 || order[0] != "domain" || order[1] != "session" {
				t.Fatalf("err=%v lookup order=%v", err, order)
			}
		})
	}
}

func TestResolveOriginCollectionMapsLookupFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"site missing", sql.ErrNoRows, "site_not_found"},
		{"lookup failed", errors.New("lookup failed"), "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := New(nil, time.Time{}, Deps{ResolveNamedSite: func(context.Context, string) (string, error) {
				calls++
				return "", tc.err
			}})
			_, err := a.resolveOriginCollection(context.Background(), CollectionListSiteInput{
				OwnerKeyInput: OwnerKeyInput{SiteName: "site"}, Collection: "items", OriginAllowed: true,
			})
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.code || calls != 1 {
				t.Fatalf("err=%v calls=%d, want %s and one lookup", err, calls, tc.code)
			}
		})
	}
}
