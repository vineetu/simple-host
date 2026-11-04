package hackteams

import (
	"strconv"
	"strings"
)

// DefaultMaxTeamSlugLen is the longest a team slug may be on the two-level
// <team>.<event>.<host> form (each label has its own 63-character DNS
// budget, so a team slug never has to share it with the event's).
const DefaultMaxTeamSlugLen = 30

// MaxTeamSlugLen is the longest a team slug may be: DefaultMaxTeamSlugLen
// normally, or, under the flat <team>--<event> single-label host
// (TEAM_SITE_FLAT_HOSTS), capped so "<team>--<event>" never exceeds the
// 63-character DNS label limit (63, less 2 for "--", less the event
// slug's own length). Never less than 1, and never more than
// DefaultMaxTeamSlugLen either way.
func MaxTeamSlugLen(eventSlug string, flat bool) int {
	if !flat {
		return DefaultMaxTeamSlugLen
	}
	max := 63 - 2 - len(eventSlug)
	if max > DefaultMaxTeamSlugLen {
		max = DefaultMaxTeamSlugLen
	}
	if max < 1 {
		max = 1
	}
	return max
}

// DeriveTeamSlug converts a team name to a slug base, at most max
// characters (MaxTeamSlugLen). It collapses every run of non-alphanumeric
// characters to a single "-" (the dash guard below), so a derived slug can
// never contain "--" — the separator TEAM_SITE_FLAT_HOSTS' <team>--<event>
// single label relies on being unambiguous. TeamSlugCandidate's own "-<n>"
// suffix preserves that: its root is hyphen-trimmed first, so the suffix
// never follows another hyphen.
func DeriveTeamSlug(name string, max int) string {
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
	if max < 1 {
		max = 1
	}
	if len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	if out == "" {
		out = "group"
		if len(out) > max {
			out = out[:max]
		}
	}
	return out
}

// TeamSlugCandidate returns a numbered candidate, at most max characters.
func TeamSlugCandidate(base string, n, max int) string {
	if max < 1 {
		max = 1
	}
	if n == 0 {
		if len(base) > max {
			return base[:max]
		}
		return base
	}
	suf := "-" + strconv.Itoa(n+1)
	keep := max - len(suf)
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
