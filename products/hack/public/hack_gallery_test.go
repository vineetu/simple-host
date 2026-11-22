package public

import (
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/core"
)

func TestHackScreenshotPath(t *testing.T) {
	for _, slug := range []string{"a", "ab", "a-b", "a0", "a--b", strings.Repeat("a", 30)} {
		got, ok := hackScreenshotPath("/screenshots/" + slug)
		if !ok || got != slug {
			t.Errorf("slug %q: got %q ok=%v", slug, got, ok)
		}
	}
	for _, path := range []string{
		"/screenshots",
		"/screenshots/",
		"/screenshots/ab/extra",
		"/screenshots/../etc/passwd",
		"/screenshots/%2e%2e",
		"/screenshots/foo%2fbar",
		"/screenshots/Ab",
		"/screenshots/a_b",
		"/screenshots/-ab",
		"/screenshots/" + strings.Repeat("a", 31),
		"/screenshots/ab%00cd",
	} {
		if _, ok := hackScreenshotPath(path); ok {
			t.Errorf("accepted %q", path)
		}
	}
}
func TestHackGalleryLink(t *testing.T) {
	ok := func(event, team string) string {
		return "https://" + team + "." + event + ".simple-hack.test/"
	}
	if got := core.GalleryLink(ok, "weekend", "alpha"); got != "https://alpha.weekend.simple-hack.test/" {
		t.Fatalf("link %q", got)
	}
	for _, c := range []struct {
		event, team string
		build       func(string, string) string
	}{
		{"Weekend", "alpha", ok},
		{"weekend", "Alpha", ok},
		{"weekend", "../x", ok},
		{"week.end", "alpha", ok},
		{"", "alpha", ok},
		{"weekend", "", ok},
		{"weekend", strings.Repeat("a", 31), ok},
		{"weekend", "alpha", func(string, string) string { return "javascript:alert(1)" }},
		{"weekend", "alpha", func(string, string) string { return "https://evil.example/" }},
		{"weekend", "alpha", func(string, string) string { return "https://user:pass@alpha.weekend.simple-hack.test/" }},
		{"weekend", "alpha", nil},
	} {
		if got := core.GalleryLink(c.build, c.event, c.team); got != "" {
			t.Errorf("event %q team %q built %q", c.event, c.team, got)
		}
	}
}

// TestHackGalleryLinkFlatHosts: a builder returning the flat
// <team>--<event>.<base> single label (TEAM_SITE_FLAT_HOSTS) is accepted
// exactly like the two-level form; a host that belongs to neither shape is
// still refused.
func TestHackGalleryLinkFlatHosts(t *testing.T) {
	flat := func(event, team string) string {
		return "https://" + team + "--" + event + ".simple-hack.test/"
	}
	if got := core.GalleryLink(flat, "weekend", "alpha"); got != "https://alpha--weekend.simple-hack.test/" {
		t.Fatalf("flat link %q", got)
	}
	wrongTeam := func(string, string) string { return "https://someone-else--weekend.simple-hack.test/" }
	if got := core.GalleryLink(wrongTeam, "weekend", "alpha"); got != "" {
		t.Errorf("accepted a flat host for another team: %q", got)
	}
}
