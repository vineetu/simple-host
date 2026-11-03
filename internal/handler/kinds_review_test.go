package handler

import "testing"

// TestHostRetainsLegacyStorageReview moved out of
// internal/hackhttp/hack_storage_only_review_test.go (cleanup 3.2 slice 3):
// it is 100% Host-only (no HackHandler, no team site), so it stays here
// with its fixture (kindsSite, kinds_test.go) rather than crossing the
// package boundary for no reason.
func TestHostRetainsLegacyStorageReview(t *testing.T) {
	s := newKindsSite(t, false)
	wantCode(t, "Host state", s.owner(t, "GET", "/v1/sites/shop/state", nil), 200, "")
	wantCode(t, "Host declared data", s.owner(t, "GET", "/v1/sites/shop/data", nil), 200, "")
}
