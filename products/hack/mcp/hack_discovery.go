package hackmcp

import (
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

// hackDiscovery is Simple Hack's short tool list, as Simple Host's own: the
// first moves of each role are listed, and find_tools and run_tool reach the
// rest under the same checks. ChatGPT still lists every tool.
func hackDiscovery() *mcp.ProductDiscovery {
	return &mcp.ProductDiscovery{
		Core: hackCoreTools, Aliases: hackToolAliases,
		Instructions: hackMoreToolsInstructions,
		Tool:         hackDiscoveryTool,
	}
}

// hackCoreTools are the tools a session needs first, by role, chosen from the
// eval scenarios (scripts/mcpeval/hack.yaml) and the skills' flows.
var hackCoreTools = []string{
	// Everyone: where am I, and what is next.
	"hack_list_events", "hack_get_event", "hack_get_checklist", "hack_get_my_todos",
	// Organiser: create and open an event, set it up, follow it, finish it.
	"hack_check_event_name", "hack_create_event", "hack_set_event_stage",
	"hack_get_registration", "hack_set_registration", "hack_get_tracks", "hack_set_tracks",
	"hack_get_entry_rules", "hack_set_entry_rules", "hack_get_rubric", "hack_set_rubric",
	"hack_get_teams", "hack_post_announcement", "hack_publish_results",
	// Participant: join, make a team, publish its site, hand in the entry.
	"hack_preview_join", "hack_join_event", "hack_create_team", "hack_get_my_teams",
	"hack_get_entry", "hack_update_entry", "hack_submit_entry",
	"list_sites", "get_site", "read_site_file", "create_site", "update_site",
	// Judge: join, read the queue, score, declare a conflict.
	"hack_join_judge", "hack_get_judge_queue", "hack_score_team", "hack_declare_my_conflict",
}

// hackDiscoveryTool words find_tools and run_tool for Simple Hack.
func hackDiscoveryTool(tool mcp.Tool) mcp.Tool {
	tool.Title = strings.Replace(tool.Title, "Simple Host", "Simple Hack", 1)
	switch tool.Name {
	case "find_tools":
		tool.Description = "Find tools for a job: event details, schedule, page and icon, applications, people, teams and team links, messages, voting, judges, panels and assignments, exports, results, leaving, site versions, domains and saved data. Returns matching tools and arguments; call your pick with run_tool."
	case "run_tool":
		tool.Description = "Run a Simple Hack tool by name with its arguments, from find_tools or a description, skill, recipe or result naming a tool outside your list. The same checks apply; ask the person first where that tool's description says so."
	}
	return tool
}

// hackToolAliases are the job words people use for the tools outside the core
// list, for find_tools.
var hackToolAliases = map[string][]string{
	"hack_update_event":                {"edit event", "change event", "rename event", "event dates", "schedule", "venue", "location", "prizes", "event description", "change date"},
	"hack_get_navigation":              {"menu", "what can i do", "navigation"},
	"hack_get_public_event":            {"public page", "event page", "what visitors see"},
	"hack_get_directory":               {"find hackathons", "public events", "browse events", "directory"},
	"hack_set_directory_listing":       {"list publicly", "hide from directory", "show in directory"},
	"hack_get_content":                 {"event page text", "faq", "about text", "event content"},
	"hack_set_content":                 {"edit event page", "faq", "about text", "event content", "page text"},
	"hack_set_event_icon":              {"logo", "event picture", "icon"},
	"hack_clear_event_icon":            {"remove logo", "remove icon"},
	"hack_set_event_website_mode":      {"event website", "own website", "custom event site"},
	"hack_get_announcements":           {"announcements", "news", "updates posted"},
	"hack_edit_announcement":           {"change announcement", "fix announcement"},
	"hack_delete_announcement":         {"remove announcement"},
	"hack_message_team":                {"email teams", "message all teams", "send email", "email participants"},
	"hack_get_team_messages":           {"team messages", "team questions", "inbox"},
	"hack_get_my_team_messages":        {"messages from organisers", "replies", "my inbox"},
	"hack_message_organisers":          {"ask organisers", "contact organisers", "question for organisers", "help"},
	"hack_get_applications":            {"applications", "applicants", "sign-ups waiting", "pending sign-ups", "who applied"},
	"hack_decide_application":          {"approve", "accept applicant", "reject applicant", "approve sign-up", "decline applicant", "let in"},
	"hack_preview_application":         {"apply", "application form", "join by applying"},
	"hack_apply_to_event":              {"apply", "sign up", "register", "join by applying", "application"},
	"hack_application_status":          {"application status", "was i accepted", "approved yet"},
	"hack_regenerate_code":             {"new join link", "new judge link", "reset link", "join code", "judge code", "invite link"},
	"hack_get_people":                  {"participants", "who signed up", "attendees", "people", "who joined"},
	"hack_remove_person":               {"kick", "remove participant", "remove judge", "ban"},
	"hack_fill_teams":                  {"auto assign teams", "put solo people in teams", "fill teams", "match teams"},
	"hack_move_member":                 {"move person", "switch team", "change team"},
	"hack_remove_team_member":          {"remove from team", "kick from team"},
	"hack_rename_team":                 {"team name", "rename team"},
	"hack_lock_team":                   {"lock team", "freeze team", "unlock team"},
	"hack_set_team_admin":              {"team admin", "team captain", "team lead"},
	"hack_new_team_link":               {"team invite link", "team link"},
	"hack_delete_team":                 {"remove team", "delete team"},
	"hack_set_team_deadline":           {"extension", "extra time", "team deadline", "late submission"},
	"hack_take_down_team_site":         {"take down team site", "hide team site", "remove team website"},
	"hack_restore_team_site":           {"bring back team site", "restore team website"},
	"hack_create_organiser_invite":     {"co-organiser", "add organiser", "invite organiser", "co-host"},
	"hack_revoke_organiser_invite":     {"cancel organiser invite"},
	"hack_preview_organiser_invite":    {"organiser invite", "co-organiser link"},
	"hack_accept_organiser_invite":     {"accept organiser invite", "become organiser", "co-organiser link"},
	"hack_remove_organiser":            {"remove co-organiser", "remove organiser"},
	"hack_export_participants":         {"download participants", "participant list", "csv", "spreadsheet", "emails list"},
	"hack_export_teams":                {"download teams", "team list", "csv"},
	"hack_export_entries":              {"download entries", "projects list", "csv", "submissions"},
	"hack_export_scores":               {"download scores", "scores csv", "all scores", "judge comments"},
	"hack_export_results":              {"download results", "results csv", "rankings", "winners"},
	"hack_export_projects_archive":     {"download team sites", "archive projects", "zip"},
	"hack_export_own_team_archive":     {"download our site", "backup team site", "zip"},
	"hack_get_usage":                   {"usage", "limits", "storage used", "how much space"},
	"hack_get_reminder_settings":       {"reminders", "deadline emails"},
	"hack_set_reminder_settings":       {"reminders", "deadline emails", "remind participants"},
	"hack_get_preferences":             {"my settings", "preferences"},
	"hack_set_preferences":             {"my settings", "preferences", "language", "time zone"},
	"hack_set_email_updates":           {"stop emails", "email updates", "unsubscribe", "turn off emails"},
	"hack_dismiss_notices":             {"dismiss", "clear news", "hide notice"},
	"hack_set_checklist_not_needed":    {"skip step", "not needed", "checklist"},
	"hack_acknowledge_checklist_item":  {"done with step", "acknowledge", "checklist"},
	"hack_list_open_teams":             {"find a team", "teams looking", "looking for members", "join a team", "open teams"},
	"hack_ask_to_join":                 {"request to join team", "ask team", "join a team"},
	"hack_invite_to_team":              {"invite to my team", "add teammate", "recruit"},
	"hack_accept_team_request":         {"accept request", "accept invite", "let them join my team"},
	"hack_decline_team_request":        {"decline request", "reject invite"},
	"hack_withdraw_team_request":       {"cancel request", "withdraw request", "cancel invite"},
	"hack_remove_teammate":             {"remove teammate", "kick teammate"},
	"hack_set_looking":                 {"looking for team", "solo", "find teammates", "available"},
	"hack_preview_team_link":           {"team link", "team invite link"},
	"hack_join_team":                   {"join team", "team link", "team code"},
	"hack_leave_team":                  {"leave team", "quit team"},
	"hack_update_my_team":              {"rename my team", "team name", "looking for skills"},
	"hack_lock_my_team":                {"lock my team", "close my team", "unlock my team"},
	"hack_set_teammate_admin":          {"make admin", "team admin", "team captain"},
	"hack_new_my_team_link":            {"new team link", "team invite link"},
	"hack_choose_track":                {"pick track", "choose challenge", "track"},
	"hack_get_team_status":             {"team status", "my team", "deadline", "what is left"},
	"hack_preview_judge":               {"judge link", "judge invite", "become a judge"},
	"hack_preview_leave_event":         {"leave event", "drop out", "withdraw"},
	"hack_leave_event":                 {"leave event", "drop out", "withdraw", "quit hackathon"},
	"hack_unsubmit_entry":              {"unsubmit", "take back entry", "edit after submitting", "reopen entry"},
	"hack_set_entry_screenshot":        {"screenshot", "project image", "cover image", "entry picture"},
	"hack_get_entry_screenshot":        {"screenshot", "project image"},
	"hack_delete_entry_screenshot":     {"remove screenshot"},
	"hack_get_team_screenshot":         {"team screenshot", "project image"},
	"hack_get_voting":                  {"vote", "voting", "people's choice", "ballot"},
	"hack_get_my_vote":                 {"my vote", "who did i vote for"},
	"hack_vote":                        {"vote", "cast vote", "vote for a project", "vote for a team", "people's choice"},
	"hack_get_voting_settings":         {"voting settings", "people's choice"},
	"hack_set_voting_settings":         {"turn on voting", "people's choice", "audience vote", "voting settings"},
	"hack_get_my_results":              {"our results", "did we win", "my score", "feedback"},
	"hack_get_public_results":          {"results", "winners", "leaderboard"},
	"hack_set_results_view":            {"public results", "show scores", "results page"},
	"hack_get_submissions":             {"submissions", "projects", "browse projects", "gallery"},
	"hack_get_entries":                 {"entries", "projects", "who submitted", "submissions"},
	"hack_set_entry_excluded":          {"disqualify", "exclude entry", "remove from judging"},
	"hack_get_judge_scores":            {"my scores", "what did i score", "change score"},
	"hack_get_my_conflicts":            {"my conflicts"},
	"hack_remove_my_conflict":          {"remove conflict", "undo conflict"},
	"hack_get_judging_settings":        {"judging settings", "judging deadline"},
	"hack_set_judging_settings":        {"judging settings", "judging deadline", "judges per entry", "blind judging"},
	"hack_generate_assignments":        {"assign judges", "judge assignments", "who judges what", "distribute entries"},
	"hack_get_assignments":             {"judge assignments", "who judges what"},
	"hack_set_assignments":             {"assign judges", "change assignments"},
	"hack_get_panels":                  {"judging panels", "judge groups"},
	"hack_set_panels":                  {"judging panels", "judge groups"},
	"hack_preview_judging_assignments": {"preview judging", "preview assignments"},
	"hack_get_conflicts":               {"conflicts", "judge conflicts"},
	"hack_set_judge_conflict":          {"judge conflict", "recuse judge"},
	"hack_remove_judge_conflict":       {"remove judge conflict"},
	"hack_get_judging_dashboard":       {"judging progress", "how is judging going", "scores so far", "who has not judged"},
	"hack_delete_former_judge_scores":  {"delete departed judge scores", "former judge"},
	"hack_get_page_recipe":             {"form", "sign-up form", "booking", "page code", "saved data", "visitor uploads", "recipe"},
	"hack_delete_event":                {"delete event", "cancel hackathon", "archive event"},
	"list_sites":                       {"my sites", "websites", "team sites"},
}
