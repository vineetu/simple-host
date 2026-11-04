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
// <team>--<event>, back into its slugs: the team is everything before the
// FIRST "--", the event everything after it. A team slug may never contain
// "--" (internal/rules/hackteams.DeriveTeamSlug's hyphen-run collapsing), so
// splitting on the first occurrence always isolates it correctly. An event
// slug created after "--" was forbidden there (internal/hackhttp's
// slugVerdict) cannot contain "--" either, but a legacy event slug from
// before that rule can — this still accepts it as the event, "--" and all,
// rather than rejecting it as ambiguous: callers (handler/sitebase.go's
// siteHostParts) have already ruled out the whole label being a real
// account handle before reaching here, so there is no longer an event to
// confuse this with. Only an empty team or event is ok=false.
func TeamSiteHostLabels(label string) (team, event string, ok bool) {
	team, event, ok = strings.Cut(label, "--")
	if !ok || team == "" || event == "" {
		return "", "", false
	}
	return team, event, true
}
