package auth

import (
	"regexp"
	"strings"
)

// OAuthScope is the base scope this server issues: every grant includes it.
const OAuthScope = "sites"

// HackTeamScopePrefix marks a grant bound to one team's site. The scope is
// OAuthScope plus "team:<team id>". Only this server writes it.
const HackTeamScopePrefix = "team:"

// HackEventsScope marks a grant that acts as the person for event
// management. It is not implied by a missing team, so an old or malformed
// grant fails closed instead of becoming an unrestricted key. The word
// matches mcp.CallerModeEvents.
const HackEventsScope = "events"

// uuidShape guards the team id GrantKind extracts. Duplicated from
// internal/rules/sites.UUIDShape: this package may import only model (per
// docs/architecture/target.md §2), so it cannot reach rules/sites; the
// pattern is load-bearing and only ever changes with the uuid column type.
var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ScopeTeam returns the team a grant scope binds to ("" for none).
func ScopeTeam(scope string) string {
	for _, f := range strings.Fields(scope) {
		if t, ok := strings.CutPrefix(f, HackTeamScopePrefix); ok {
			return t
		}
	}
	return ""
}

func scopeHas(scope, want string) bool {
	for _, f := range strings.Fields(scope) {
		if f == want {
			return true
		}
	}
	return false
}

// GrantKind classifies a hosted grant. ok is false when the scope names
// both a team and event management, or neither: an old "sites" grant, or
// anything else that is not exactly one of the two choices.
func GrantKind(scope string) (teamID string, events bool, ok bool) {
	// The server issues exactly sites plus one valid identity marker. Do not
	// turn malformed team grants into personal event-management grants.
	if len(strings.Fields(scope)) != 2 || !scopeHas(scope, OAuthScope) {
		return "", false, false
	}
	if scopeHas(scope, HackEventsScope) {
		return "", true, true
	}
	teamID = ScopeTeam(scope)
	if !uuidShape.MatchString(teamID) {
		return "", false, false
	}
	return teamID, false, true
}
