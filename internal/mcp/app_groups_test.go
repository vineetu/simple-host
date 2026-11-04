package mcp

// Expected screen groups, kept as test fixtures independently of listing policy.
var (
	hackAppOnlyJudgeNames     = []string{"hack_app_get_judge_queue", "hack_app_get_judge_scores", "hack_app_get_my_conflicts"}
	hackAppOnlyOrganiserNames = []string{
		"hack_app_get_people", "hack_app_get_applications", "hack_app_get_registration",
		"hack_app_get_judging_settings", "hack_app_get_conflicts", "hack_app_get_teams",
		"hack_app_get_assignments", "hack_app_get_judging_dashboard", "hack_app_set_event_stage",
		"hack_app_regenerate_code", "hack_app_create_organiser_invite",
	}
	hackAppOnlySharedNames = []string{"hack_app_get_rubric"}
)
