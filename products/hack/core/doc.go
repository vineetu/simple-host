// Package core is Simple Hack's door, the part every feature calls: who the
// caller is in an event and what they may do (LoadMember over members.Caller),
// the one refusal writer (WriteError), the links to the platform's team sites
// (Links, behind the SiteHooks the composition sets), and the JSON views
// several features answer with (EventView, entry list items, team blocks).
// Hack is the module's shared state: the app services, the limits and the one
// instance of each rate limiter. It imports no feature.
package core
