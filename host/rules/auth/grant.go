package auth

import "strings"

// OAuthScope is the base scope this server issues: every grant includes it.
const OAuthScope = "sites"

// HackEventsScope marks a grant that acts as the person for event
// management. It is not implied by a missing team, so an old or malformed
// grant fails closed instead of becoming an unrestricted key. The word
// matches mcp.CallerModeEvents.
const GroupManagementScope = "events"

// EventsGrant reports whether a hosted grant is a product connection: the
// server issues exactly OAuthScope plus GroupManagementScope. Anything else
// (an old team-bound grant, a malformed scope) fails closed.
func EventsGrant(scope string) bool {
	return len(strings.Fields(scope)) == 2 && scopeHas(scope, OAuthScope) && scopeHas(scope, GroupManagementScope)
}

func scopeHas(scope, want string) bool {
	for _, f := range strings.Fields(scope) {
		if f == want {
			return true
		}
	}
	return false
}
