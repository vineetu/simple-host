package hackteams

import (
	"strconv"
	"strings"
)

// DeriveTeamSlug converts a team name to a slug base. It collapses every run
// of non-alphanumeric characters to a single "-" (the dash guard below), so
// a derived slug can never contain "--" — the separator
// TEAM_SITE_FLAT_HOSTS' <team>--<event> single label relies on being
// unambiguous. TeamSlugCandidate's own "-<n>" suffix preserves that: its
// root is hyphen-trimmed first, so the suffix never follows another hyphen.
func DeriveTeamSlug(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 30 {
		out = strings.Trim(out[:30], "-")
	}
	if out == "" {
		out = "group"
	}
	return out
}

// TeamSlugCandidate returns a numbered candidate.
func TeamSlugCandidate(base string, n int) string {
	if n == 0 {
		if len(base) > 30 {
			return base[:30]
		}
		return base
	}
	suf := "-" + strconv.Itoa(n+1)
	keep := 30 - len(suf)
	if keep < 1 {
		keep = 1
	}
	root := base
	if len(root) > keep {
		root = strings.Trim(root[:keep], "-")
	}
	if root == "" {
		root = "g"
	}
	return root + suf
}
