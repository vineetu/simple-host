package handler

import "testing"

func TestLegacyStoragePath(t *testing.T) {
	paths := []string{
		"/v1/sites/team/state", "/v1/sites/team/state/history/1",
		"/v1/sites/team/collections/entries/items/1", "/v1/sites/team/data/name/kind",
		"/v1/sites/team/savers/block", "/v1/sites/team/history",
		"/v1/sites/team/allowed-origins", "/v1/sites/team/allow-anonymous-writes",
		"/v1/u/owner/sites/team/state", "/v1/u/owner/sites/team/collections/entries",
		"/v1/u/owner/sites/team/data/name/history/1", "/v1/u/owner/sites/team/history",
		"/v1/admin/sites/id/collections/private/export.csv", "/v1/admin/data-watch",
		"/v1/data-notify/stop",
	}
	for _, path := range paths {
		if !LegacyStoragePath(path) {
			t.Errorf("legacy path missed: %s", path)
		}
	}
	for _, path := range []string{
		"/v1/sites/team/storage/resources", "/v1/sites/team/storage/kv/settings/keys/theme",
		"/v1/sites/team/visitor/auth", "/v1/sites/team/me", "/v1/sites/team/lock",
		"/v1/hack/events/event/website/storage/resources", "/v1/hack/events/event/state",
		"/v1/admin/sites/id/versions", "/v1/visitor/establish",
	} {
		if LegacyStoragePath(path) {
			t.Errorf("current route blocked: %s", path)
		}
	}
}

func TestParseLegacyStoragePath(t *testing.T) {
	cases := []struct {
		path                  string
		match, skip           bool
		handle, site, adminID string
	}{
		{"/v1/sites/shop/state", true, false, "", "shop", ""},
		{"/v1/sites/shop/collections/guestbook", true, false, "", "shop", ""},
		{"/v1/sites/shop/data/notes/kind", true, false, "", "shop", ""},
		{"/v1/u/olive/sites/shop/state", true, false, "olive", "shop", ""},
		{"/v1/u/olive/sites/shop/collections/guestbook", true, false, "olive", "shop", ""},
		{"/v1/admin/sites/abc/collections", true, false, "", "", "abc"},
		{"/v1/admin/sites/abc/collections/private/export.csv", true, false, "", "", "abc"},
		{"/v1/data-notify/stop", true, true, "", "", ""},
		{"/v1/admin/data-watch", true, true, "", "", ""},
		{"/v1/sites/shop/storage/resources", false, false, "", "", ""},
		{"/v1/sites/shop/files", false, false, "", "", ""},
		{"/v1/me", false, false, "", "", ""},
	}
	for _, c := range cases {
		got := parseLegacyStoragePath(c.path)
		if got.match != c.match || got.skip != c.skip || got.handle != c.handle || got.site != c.site || got.adminID != c.adminID {
			t.Errorf("%s: %+v, want match=%v skip=%v handle=%q site=%q adminID=%q",
				c.path, got, c.match, c.skip, c.handle, c.site, c.adminID)
		}
		if LegacyStoragePath(c.path) != c.match {
			t.Errorf("LegacyStoragePath(%s) = %v, want %v", c.path, !c.match, c.match)
		}
	}
}
