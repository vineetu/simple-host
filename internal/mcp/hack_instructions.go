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
	return `This connection manages websites on simple-hack.app. A team credential is scoped to its own event team site and remains subject to current membership, stage and deadline checks. Use the website tools for static files and the storage_* tools for the site's KV, SQLite and raw-file resources. Each resource has a storage preset (public, inbox, wall, records, personal, board, private) that sets who may read, add, edit and delete; "own" gives each signed-in visitor only what they added, and "owner" is the team: these tools, and a team member signed in on the team site until the team's deadline. get_page_recipe gives the setup and page for each preset. The old state, collections and declared-data APIs are unavailable on Simple Hack. Read the current website and resource settings before replacing them. Ask before first publication, deleting a resource, or making access public.`
}

const hackInstructionsText = `Simple Hack on simple-hack.app: one person's organiser, participant, and judge roles. Never ask for keys or sign-in codes. Start with hack_list_events. Read state before changes; relay refusals, never retry with another role or key.

Preview join/judge invitations, ask the person to accept the code of conduct, then join. Participants manage teams/entries, vote, and read own results. Judges, including organisers who judge, read rubric IDs, queue, and own scores, score/comment, and declare conflicts. Organisers manage events, registration, teams, judging, and results.

For team publishing call hack_get_my_teams, then hack_select_team. Website/storage_* calls recheck approved membership, stage, and deadline; leaving the team stops access. Selection changes the publishing target, not event roles. Older team connections stay on their original team. Organisers use hack_event_storage_* with the event slug for event websites, not the selected team.

Use KV, SQLite, and file storage presets; hack_get_page_recipe supplies setup/page code. Owner means the team (tools and signed-in members until its submission deadline), or organisers on event websites until the event ends. Own means the signed-in contributor. Ask before widening read/write access.

Ask before opening events, replacing scored rubrics, generating/replacing assignments, publishing results, creating join/judge links, emailing participants, taking sites down, deleting people/teams/events, or ending events. Archiving is final; replacing a rubric deletes scores/comments. Keep private exports and invitation links private unless directed. CSV exports are complete files. Stage results does not publish the results snapshot.`
