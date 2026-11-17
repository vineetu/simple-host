package handler

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestBulkUsernames(t *testing.T) {
	for _, n := range []int{-1, 0} {
		if _, err := bulkUsernames(bulkUsersRequest{Count: ptr(n)}); err == nil {
			t.Fatalf("accepted count %d", n)
		}
	}
	for _, n := range []int{1, 30, 200} {
		names, err := bulkUsernames(bulkUsersRequest{Count: ptr(n), Prefix: ptr("team")})
		if err != nil || len(names) != n || names[0] != "team-01" {
			t.Fatalf("count %d: %v %v", n, names, err)
		}
		if n == 200 && names[199] != "team-200" {
			t.Fatal(names[199])
		}
	}
	// No compiled-in ceiling. Ten thousand is a legal request; whether this
	// instance can hold them is a question about its disk, answered separately
	// by accountsAvailable with the real number.
	big, err := bulkUsernames(bulkUsersRequest{Count: ptr(10000), Prefix: ptr("team")})
	if err != nil || len(big) != 10000 {
		t.Fatalf("count 10000: %v (%d names)", err, len(big))
	}
	if big[0] != "team-01" || big[9999] != "team-10000" {
		t.Fatalf("first %q last %q", big[0], big[9999])
	}
	// Every generated name must be a legal handle and distinct from every
	// other: each becomes a directory and a unique row, so a collision at this
	// size is ten thousand keys issued and somebody silently without an account.
	seen := make(map[string]bool, len(big))
	for _, name := range big {
		if seen[name] {
			t.Fatalf("duplicate account name %q", name)
		}
		seen[name] = true
		if err := validateHandle(name); err != nil {
			t.Fatalf("generated name %q is not a usable handle: %v", name, err)
		}
	}
	names, err := bulkUsernames(bulkUsersRequest{Count: ptr(1)})
	if err != nil || names[0] != "guest-01" {
		t.Fatal(names, err)
	}
	for _, req := range []bulkUsersRequest{{}, {Emails: ptr([]string{})}, {Emails: ptr([]string{"bad"})}, {Count: ptr(1), Emails: ptr([]string{"a@x.com"})}, {Emails: ptr([]string{"a@x.com"}), Prefix: ptr("team")}, {Count: ptr(1), Prefix: ptr("../bad")}} {
		if _, err := bulkUsernames(req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	names, err = bulkUsernames(bulkUsersRequest{Emails: ptr([]string{" A@X.COM "})})
	if err != nil || names[0] != "a@x.com" {
		t.Fatal(names, err)
	}
}

func TestRequestedCountValidatesShapeWithoutBuildingTheAnswer(t *testing.T) {
	// The capacity check runs on this number, so it has to be right before a
	// slice the size of the request is allocated. A request for a billion
	// accounts must be counted and refused, not counted by allocating it.
	if n, err := requestedCount(bulkUsersRequest{Count: ptr(1_000_000_000)}); err != nil || n != 1_000_000_000 {
		t.Errorf("requestedCount = %d, %v", n, err)
	}
	if n, err := requestedCount(bulkUsersRequest{Emails: ptr([]string{"a@x.com", "b@x.com"})}); err != nil || n != 2 {
		t.Errorf("requestedCount = %d, %v", n, err)
	}
	// Same shape rules as bulkUsernames, or the two would disagree about what
	// a valid request is.
	for _, req := range []bulkUsersRequest{{}, {Count: ptr(1), Emails: ptr([]string{"a@x.com"})}, {Emails: ptr([]string{"a@x.com"}), Prefix: ptr("team")}} {
		if _, err := requestedCount(req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}

func TestProfileValidation(t *testing.T) {
	for _, handle := range []string{"admin", "by-id", "UPPER", "a/b", "", strings.Repeat("a", 40)} {
		if validateHandle(handle) == nil {
			t.Fatalf("accepted %q", handle)
		}
	}
	for handle := range reservedHandles {
		if validateHandle(handle) == nil {
			t.Fatal(handle)
		}
	}
	if err := validateHandle("team-01"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"   ", " Alice ", strings.Repeat("界", 100)} {
		got, err := normalizeDisplayName(name)
		if err != nil || got != strings.TrimSpace(name) {
			t.Fatal(got, err)
		}
	}
	if _, err := normalizeDisplayName(strings.Repeat("界", 101)); err == nil {
		t.Fatal("accepted long name")
	}
}
