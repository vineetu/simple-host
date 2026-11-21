package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// HackInstructions describes the one hosted personal connection: every tool acts
// as the person, and the platform checks membership, stage and deadline.
func HackInstructions() string {
	return hackInstructionsText
}

const hackInstructionsText = `Simple Hack on simple-hack.app: one person's organiser, participant, and judge roles. Never ask for keys or sign-in codes. Start with hack_list_events. Read state before changes; relay refusals, never retry with another role or key.

Preview invitations; get code of conduct acceptance first. Participants find teams, fill entry cards, then hack_submit_entry, vote and read results. Judges (organisers too) read rubric IDs, queue and own scores; score, comment, declare conflicts. Organisers run events, registration, teams, hack_set_entry_rules, judging and results; preview fill teams before applying.

Publish: website/storage_* tools take site (hack_get_my_teams) and owner (event name). Calls recheck membership, stage and deadline. Organisers publish the event website (site: organiser.website_site, hack_get_event) and only read team sites.

` + mcp.StorageRulesInstructions + ` hack_get_page_recipe supplies setup/page code. Owner: teammates until the submission deadline, organisers until the event ends.

Ask before opening events, replacing scored rubrics, generating/replacing assignments, publishing results, creating join/judge links, emailing participants, taking sites down, deleting people/teams/events, ending or leaving events. Archiving is final. Rubric saves keep scores unless you confirm deletions. Keep exports and invite links private. Stage results does not publish results.`

// hackMoreToolsInstructions follows the instructions on a connection that
// lists the short tool list (every app but ChatGPT, which lists every tool).
const hackMoreToolsInstructions = "MORE TOOLS: find_tools, then run_tool, which also calls unlisted tools named in descriptions or results."
