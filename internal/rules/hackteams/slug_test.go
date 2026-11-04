package hackteams

import (
	"strings"
	"testing"
)

// TestDeriveTeamSlugCollapsesHyphenRuns: a derived team slug can never
// contain "--", whatever the input throws at it — the separator
// TEAM_SITE_FLAT_HOSTS' <team>--<event> single label relies on being
// unambiguous.
func TestDeriveTeamSlugCollapsesHyphenRuns(t *testing.T) {
	cases := map[string]string{
		"a---b":          "a-b",
		"Team -- Rocket": "team-rocket",
		"a_b__c---d":     "a-b-c-d",
		"  leading":      "leading",
		"trailing  ":     "trailing",
		"":               "group",
		"----":           "group",
	}
	for in, want := range cases {
		if got := DeriveTeamSlug(in, DefaultMaxTeamSlugLen); got != want {
			t.Errorf("DeriveTeamSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTeamSlugCandidateNeverDoublesHyphens(t *testing.T) {
	base := DeriveTeamSlug("Team Rocket", DefaultMaxTeamSlugLen)
	for n := 0; n < 5; n++ {
		got := TeamSlugCandidate(base, n, DefaultMaxTeamSlugLen)
		if got == "" {
			t.Fatalf("candidate %d of %q is empty", n, base)
		}
		for i := 0; i+1 < len(got); i++ {
			if got[i] == '-' && got[i+1] == '-' {
				t.Errorf("candidate %d of %q = %q contains \"--\"", n, base, got)
			}
		}
	}
}

// TestMaxTeamSlugLen: off (the two-level host), the cap is always
// DefaultMaxTeamSlugLen (30) regardless of the event slug. Under flat hosts,
// the cap leaves exactly enough room for "<team>--<event>" to fit in one
// 63-character DNS label.
func TestMaxTeamSlugLen(t *testing.T) {
	if got := MaxTeamSlugLen("x", false); got != DefaultMaxTeamSlugLen {
		t.Errorf("off: MaxTeamSlugLen = %d, want %d", got, DefaultMaxTeamSlugLen)
	}
	if got := MaxTeamSlugLen(strings.Repeat("e", 39), false); got != DefaultMaxTeamSlugLen {
		t.Errorf("off, long event slug: MaxTeamSlugLen = %d, want %d", got, DefaultMaxTeamSlugLen)
	}
	for _, eventLen := range []int{1, 10, 39} {
		event := strings.Repeat("e", eventLen)
		max := MaxTeamSlugLen(event, true)
		if got := len(event) + 2 + max; got > 63 {
			t.Errorf("flat, event %d chars: team--event is %d chars, over the 63-char DNS label limit (max=%d)", eventLen, got, max)
		}
		if max < 1 {
			t.Errorf("flat, event %d chars: max = %d, must be at least 1", eventLen, max)
		}
		if max > DefaultMaxTeamSlugLen {
			t.Errorf("flat, event %d chars: max = %d, must never exceed the off-mode default %d", eventLen, max, DefaultMaxTeamSlugLen)
		}
	}
	// A short event slug still caps at the off-mode default — flat mode
	// never makes team slugs longer than today's.
	if got := MaxTeamSlugLen("abc", true); got != DefaultMaxTeamSlugLen {
		t.Errorf("flat, short event: MaxTeamSlugLen = %d, want %d", got, DefaultMaxTeamSlugLen)
	}
}

// TestDeriveTeamSlugRespectsFlatCap: a long team name, derived under the
// flat cap for a long event slug, still produces "<team>--<event>" that
// fits in 63 characters, and candidates with collision suffixes stay
// within the same budget.
func TestDeriveTeamSlugRespectsFlatCap(t *testing.T) {
	event := strings.Repeat("e", 39) // the longest an event slug may be
	max := MaxTeamSlugLen(event, true)
	name := strings.Repeat("Rocket Team ", 10) // far longer than any cap
	base := DeriveTeamSlug(name, max)
	if len(base)+2+len(event) > 63 {
		t.Fatalf("base %q (%d chars) + -- + event (%d chars) exceeds 63", base, len(base), len(event))
	}
	for n := 0; n < 5; n++ {
		got := TeamSlugCandidate(base, n, max)
		if full := len(got) + 2 + len(event); full > 63 {
			t.Errorf("candidate %d = %q: team--event is %d chars, over 63", n, got, full)
		}
	}
}
