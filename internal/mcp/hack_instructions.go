package mcp

// HackInstructions describes the one hosted personal connection. Event tools
// use the person's identity and website tools use a selected team's scoped
// publishing identity, checked against current membership on every request.
func HackInstructions() string {
	return hackInstructionsText
}

// HackSiteInstructions is the website-only view used by older team
// connections and platform administration on hosted Simple Hack.
func HackSiteInstructions() string {
	return `This connection manages websites on simple-hack.app. A team credential is scoped to its own event team site and remains subject to current membership, stage and deadline checks. Use the website tools for static files and the storage_* tools for the site's KV, SQLite and raw-file resources. Each resource has its own read and write policy; signed-in access covers the whole resource, not separate records per visitor. The old state, collections and declared-data APIs are unavailable on Simple Hack. Read the current website and resource settings before replacing them. Ask before first publication, deleting a resource, or making access public.`
}

const hackInstructionsText = `This is the person's Simple Hack connection on simple-hack.app. It works across all their events and roles: organiser, participant and judge. Never ask them for an API key. Start with hack_list_events to see each event and role. The REST handlers enforce permissions on each tool call; a refusal means the person cannot do that action.

Use hack_preview_join or hack_preview_judge to read an invitation, then ask the person to accept the code of conduct and call the matching join tool. Participants can manage their team and entry, vote, and read their own results. Judges and organisers who judge can read the rubric (including criterion IDs), queue and own scores, score and comment, and declare conflicts. Organisers can manage event content, registration, teams, judging and results. Read current state before changing it.

The same connection can publish a team site and configure its KV, SQLite and file resources. Call hack_get_my_teams to find the person's current team_id and hack_select_team to choose it; then use the website and storage_* tools. The server checks approved participant membership again on every website call. Switching team changes the publishing target, not the person's event role. If the person leaves the team, website calls stop working. A team connection made before this unified flow remains scoped to its original team. An organiser's custom event website uses hack_event_storage_* tools with the event slug, through the person's organiser role; it does not use the selected team.

Ask before opening an event, replacing a scored rubric, generating or replacing assignments, publishing results, making new join or judge links, emailing participants, taking sites down, deleting people, teams or events, or ending an event. Ending (stage archived) is final. Replacing the rubric deletes existing scores and comments. Do not publish a person's private exports or invitation links without their direction.

CSV export tools return complete files. An event's public stage and results are separate: setting stage results does not publish the results snapshot. If a tool returns an HTTP error, relay that reason; do not retry a denied action with another role or key.`
