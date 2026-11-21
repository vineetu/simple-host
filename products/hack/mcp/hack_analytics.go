package hackmcp

import (
	"context"
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

// EventFact is what a usage event may learn of the event a call names.
type EventFact struct {
	// Found is false when no event has that name.
	Found bool
	// Role is the caller's role in the event: organiser, participant,
	// judge, or "none" when not a member.
	Role, Stage string
	// Listed is true for an event listed in Find a hackathon; Held for the
	// event whose account holds the team website the caller connected to.
	Listed, Held bool
}

// EventFacts finds the event a call names: by slug, or else the one whose
// account holds a team website (account).
type EventFacts func(ctx context.Context, slug, account, user string) EventFact

// AnalyticsTags tags each tool call's usage event with the caller's role in
// the event the call is about and that event's stage, read when the event
// is sent. The event is the call's slug, else the owner it named (a team
// website call names its event), else the one holding the connected team
// site. The tags are sent only when the caller may see the event: a member,
// a team website's connection, or an event listed publicly. Otherwise, as
// for a call about no event or a name no event has, neither is sent, so a
// refused call never tells AgentCat an unlisted event's stage.
func AnalyticsTags(facts EventFacts) func(context.Context, mcp.Caller, string, map[string]any) map[string]string {
	return func(ctx context.Context, caller mcp.Caller, _ string, args map[string]any) map[string]string {
		slug, _ := args["slug"].(string)
		if strings.TrimSpace(slug) == "" {
			slug, _ = args["owner"].(string)
		}
		fact := facts(ctx, strings.ToLower(strings.TrimSpace(slug)), caller.Owner, caller.UserID)
		member := fact.Role != "" && fact.Role != "none"
		if !fact.Found || !(member || fact.Listed || fact.Held) {
			return nil
		}
		return map[string]string{"role": fact.Role, "stage": fact.Stage}
	}
}
