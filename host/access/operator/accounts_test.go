package operator

import (
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
)

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
	// Every generated name must be a legal handle and distinct: a collision
	// means a key issued to somebody without an account.
	big, err := bulkUsernames(bulkUsersRequest{Count: ptr(2000), Prefix: ptr("team")})
	if err != nil || len(big) != 2000 {
		t.Fatalf("count 2000: %v (%d names)", err, len(big))
	}
	seen := make(map[string]bool, len(big))
	for _, name := range big {
		if seen[name] {
			t.Fatalf("duplicate account name %q", name)
		}
		seen[name] = true
		if err := accounts.CheckNewHandle(name); err != nil {
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

func ptr[T any](v T) *T { return &v }
