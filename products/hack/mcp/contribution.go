package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// Contribution is everything Simple Hack adds to the tool server: its four
// base tool families in listing order, the notification tools, the variants
// of the platform's website tools on a team-site connection, the
// instructions, the product's wording, its screens-in-chat with their
// app-only family (hack_apps.go) and the answer for its retired tool names.
func Contribution() mcp.Contribution {
	return mcp.Contribution{
		Families: []mcp.FamilyTools{
			{Family: "hack-events", Tools: append(hackEventTools(), hackChecklistTools()...)},
			{Family: "hack-entries", Tools: hackEntryTools()},
			{Family: "storage", Tools: hackStorageTools()},
			{Family: "sites", Tools: hackWebTools()},
		},
		Notifications: hackNotificationTools(),
		SiteVariants: map[string]func(mcp.Tool, mcp.Limits) mcp.Tool{
			"create_site": hackSiteCreateSite, "update_site": hackSiteUpdateSite,
			"delete_site": hackSiteDeleteSite, "restore_site": hackSiteRestoreSite,
			"export_site": hackSiteExportSite, "set_site_offline": hackSiteSetSiteOffline,
			"set_visibility": hackSiteSetVisibility, "create_upload_link": hackSiteCreateUploadLink,
			"get_page_recipe":     hackSiteGetPageRecipe,
			"storage_sql_execute": hackSiteStorageSqlExecute, "storage_get_usage": hackSiteStorageGetUsage, "who_am_i": hackSiteWhoAmI,
		},
		Instructions: func(mode string) string {
			switch mode {
			case mcp.CallerModeEvents:
				return HackInstructions()
			case mcp.CallerModeGroupSite:
				return HackSiteInstructions()
			}
			return ""
		},
		Rewrite: hackInstanceReplace, StorageError: hackStorageErrorMessage,
		NoTarget: func(tool string, listable bool) (string, map[string]any, bool) {
			if listable {
				return "No team selected. Call hack_get_my_teams to choose a team, or hack_list_events for your event websites.", map[string]any{"sites": []any{}, "count": 0}, false
			}
			return "Choose a team with hack_select_team before publishing its site.", nil, true
		},
		Apps: hackApps,
		MutationNames: map[string]mcp.RetiredMutation{
			"hack_lock_judging":   judgingLockRemoved,
			"hack_unlock_judging": judgingLockRemoved,
		},
	}
}

// judgingLockRemoved answers the two judging-lock tools, which left when
// locking became part of moving the event to Results.
var judgingLockRemoved = mcp.RetiredMutation{
	Message: "Judging locks when you finish judging (move to Results) and unlocks when you move the event back. [judging_lock_removed]",
	Code:    "judging_lock_removed",
}
