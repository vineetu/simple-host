package sites

import "strings"

// TeamSiteHost is the DNS host of a hosted-events team's site under base:
// the two-level <team>.<event>.<base> served today, or, with flat true
// (TEAM_SITE_FLAT_HOSTS), the single label <team>--<event>.<base> — so one
// wildcard certificate (*.<base>) and one ingress rule cover every event and
// team instead of a certificate SAN and a Helm upgrade per event. This is
// the one function that knows the format; handler and hackhttp both call it
// (or TeamSiteHostLabels below) rather than building or parsing it
// themselves.
func TeamSiteHost(eventSlug, teamSlug, base string, flat bool) string {
	eventSlug = strings.ToLower(eventSlug)
	teamSlug = strings.ToLower(teamSlug)
	base = strings.ToLower(base)
	if flat {
		return teamSlug + "--" + eventSlug + "." + base
	}
	return teamSlug + "." + eventSlug + "." + base
}

// TeamSiteHostLabels splits a single DNS label of the flat team-site form,
// <team>--<event>, back into its slugs. Event and team slugs may never
// contain "--" (internal/rules/hackevents' event-slug check,
// internal/rules/hackteams.DeriveTeamSlug's hyphen-run collapsing), so a
// well-formed flat label has exactly one "--"; anything else — none, or more
// than one, which would be ambiguous — is ok=false rather than guessed at.
func TeamSiteHostLabels(label string) (team, event string, ok bool) {
	team, event, ok = strings.Cut(label, "--")
	if !ok || team == "" || event == "" || strings.Contains(event, "--") {
		return "", "", false
	}
	return team, event, true
}
