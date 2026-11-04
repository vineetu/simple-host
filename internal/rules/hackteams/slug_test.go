package hackteams

import "testing"

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
		if got := DeriveTeamSlug(in); got != want {
			t.Errorf("DeriveTeamSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTeamSlugCandidateNeverDoublesHyphens(t *testing.T) {
	base := DeriveTeamSlug("Team Rocket")
	for n := 0; n < 5; n++ {
		got := TeamSlugCandidate(base, n)
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
