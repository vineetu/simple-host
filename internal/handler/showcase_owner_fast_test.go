package handler

import (
	"strings"
	"testing"
)

// TestShowcaseOwnerFastPath pins the owner dashboard's load shape: the public
// gallery is hidden before first paint for the handle this browser signed in
// as, the page asks /v1/me and /v1/sites once each, and the owner's own page
// never downloads every account's sites (?all=1). Browser proof:
// scripts/e2e-owner-dashboard.mjs.
func TestShowcaseOwnerFastPath(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	head := page[:strings.Index(page, "</head>")]
	for _, want := range []string{"localStorage.getItem('sh-owner-handle') === '__SH_HANDLE__'", "classList.add('sh-owner-pending')", ".sh-owner-pending #public-view { display: none; }"} {
		if !strings.Contains(head, want) {
			t.Errorf("head lacks %q", want)
		}
	}
	if n := strings.Count(page, "api('GET', '/v1/me')"); n != 3 {
		// bootMe, the focus re-check, and the sign-in block's later reloads.
		t.Errorf("/v1/me requested from %d places, want 3", n)
	}
	if n := strings.Count(page, "api('GET', '/v1/sites')"); n != 2 {
		// ownSites and reloadOwnerSites after a rename or delete.
		t.Errorf("/v1/sites requested from %d places, want 2", n)
	}
	if strings.Contains(page, "me.is_admin ? '?all=1'") {
		t.Error("the owner's own page still asks for every account's sites")
	}
	header, err := staticFiles.ReadFile("static/partials/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), "['apiKey', 'sh-email-nonce', 'sh-owner-handle']") {
		t.Error("sign-out does not forget sh-owner-handle")
	}
}
