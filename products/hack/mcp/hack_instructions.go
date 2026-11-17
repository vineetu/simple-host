package hackmcp

import (
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

// HackInstructions describes the one hosted personal connection. Event tools
// use the person's identity and website tools use a selected team's scoped
// publishing identity, checked against current membership on every request.
func HackInstructions() string {
	if mcp.StorageRulesEnabled() {
		return strings.NewReplacer(
			"Preview join/judge invitations, ask the person to accept the code of conduct, then join. Participants find teams; admins approve, lock, and share links; manage entries (fill the summary card, then hack_submit_entry), vote, read results. Judges, including organisers who judge, read rubric IDs, queue, and own scores, score/comment, and declare conflicts. Organisers manage events, registration, teams, entry rules (hack_set_entry_rules), judging, and results; preview fill teams before confirming apply.",
			"Preview invitations; ask acceptance of the code of conduct before joining. Participants find teams, fill entry cards, then hack_submit_entry, vote and read results. Judges, including organisers, read rubric IDs, queue and own scores; score/comment and declare conflicts. Organisers manage events, registration, teams, hack_set_entry_rules, judging and results; preview fill teams before confirming apply.",
			"Publish via hack_get_my_teams, then hack_select_team. Website/storage_* calls recheck membership, stage, and deadline; leaving the team stops access. Selection changes only the publishing target; older connections keep their team. Organisers use hack_event_storage_* with the event slug, not the selected team.",
			"Publish: hack_get_my_teams, then hack_select_team. Website/storage_* rechecks membership, stage and deadline; leaving stops access. Selection changes only publishing; older connections keep their team. Organisers use hack_event_storage_* with the event slug.",
			" Own means the signed-in contributor. Ask before widening read/write access. Saves can notify via webhooks.", "",
			"Use KV, SQLite, and file storage presets; hack_get_page_recipe supplies setup/page code.", mcp.StorageRulesInstructions+" hack_get_page_recipe supplies setup/page code.",
		).Replace(hackInstructionsText)
	}
	return hackInstructionsText
}

// HackSiteInstructions is the website-only view used by older team
// connections and platform administration on hosted Simple Hack.
func HackSiteInstructions() string {
	text := `This connection manages websites on simple-hack.app. A team credential is scoped to its own event team site and remains subject to current membership, stage and deadline checks. Use the website tools for static files and the storage_* tools for the site's KV, SQLite and raw-file resources. Each resource has a storage preset (public, inbox, wall, records, personal, board, private) that sets who may read, add, edit and delete; "own" gives each signed-in visitor only what they added, and "owner" is the team: these tools, and a team member signed in on the team site until the team's deadline. get_page_recipe gives the setup and page for each preset. The old state, collections and declared-data APIs are unavailable on Simple Hack. Read the current website and resource settings before replacing them. Ask before first publication, deleting a resource, or making access public.`
	if mcp.StorageRulesEnabled() {
		start := strings.Index(text, "Each resource has a storage preset")
		end := strings.Index(text, "The old state")
		text = text[:start] + mcp.StorageRulesInstructions + " Owner on a team site means a current approved teammate until the submission deadline, subject to stage and take-down checks. " + text[end:]
	}
	return text
}

const hackInstructionsText = `Simple Hack on simple-hack.app: one person's organiser, participant, and judge roles. Never ask for keys or sign-in codes. Start with hack_list_events. Read state before changes; relay refusals, never retry with another role or key.

Preview join/judge invitations, ask the person to accept the code of conduct, then join. Participants find teams; admins approve, lock, and share links; manage entries (fill the summary card, then hack_submit_entry), vote, read results. Judges, including organisers who judge, read rubric IDs, queue, and own scores, score/comment, and declare conflicts. Organisers manage events, registration, teams, entry rules (hack_set_entry_rules), judging, and results; preview fill teams before confirming apply.

Publish via hack_get_my_teams, then hack_select_team. Website/storage_* calls recheck membership, stage, and deadline; leaving the team stops access. Selection changes only the publishing target; older connections keep their team. Organisers use hack_event_storage_* with the event slug, not the selected team.

Use KV, SQLite, and file storage presets; hack_get_page_recipe supplies setup/page code. Owner means teammates until the submission deadline, or event organisers until the event ends. Own means the signed-in contributor. Ask before widening read/write access. Saves can notify via webhooks.

Ask before opening events, replacing scored rubrics, generating/replacing assignments, publishing results, creating join/judge links, emailing participants, taking sites down, deleting people/teams/events, ending or leaving events. Archiving is final. Rubric saves keep scores unless you confirm deletions. Keep exports and invite links private. Stage results does not publish results.`
