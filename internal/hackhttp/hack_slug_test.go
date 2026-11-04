package hackhttp

import (
	"context"
	"testing"
)

// TestSlugVerdictRejectsDoubleHyphen: an event slug (checked on both
// hack_check_event_name and create) is refused outright when it contains
// "--", before any database lookup — the separator TEAM_SITE_FLAT_HOSTS'
// <team>--<event> single label relies on appearing nowhere else in either
// slug.
func TestSlugVerdictRejectsDoubleHyphen(t *testing.T) {
	h := &HackHandler{}
	for _, slug := range []string{"ab--cd", "--abcd", "abcd--", "a--b--c"} {
		code, _, err := h.slugVerdict(context.Background(), slug)
		if err != nil {
			t.Fatalf("slug %q: unexpected error %v", slug, err)
		}
		if code != "invalid_name" {
			t.Errorf("slug %q: code = %q, want invalid_name", slug, code)
		}
	}
}
