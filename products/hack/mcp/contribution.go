package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// Contribution is everything Simple Hack adds to the tool server: base tool
// families, notification tools, website-tool variants for a team-site
// connection, instructions, wording, screens with their app-only family, and
// the answer for retired tool names.
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
		OwnerArg: "The event's name: every team and event site belongs to its event.",
		Instructions: func(mode string) string {
			if mode == mcp.CallerModeEvents {
				return HackInstructions()
			}
			return ""
		},
		Rewrite: hackInstanceReplace, StorageError: hackStorageErrorMessage,
		Apps: hackApps,
		MutationNames: map[string]mcp.RetiredMutation{
			"hack_lock_judging":   judgingLockRemoved,
			"hack_unlock_judging": judgingLockRemoved,
		},
	}
}

// judgingLockRemoved answers the two judging-lock tools, removed when locking
// became part of moving the event to Results.
var judgingLockRemoved = mcp.RetiredMutation{
	Message: "Judging locks when you finish judging (move to Results) and unlocks when you move the event back. [judging_lock_removed]",
	Code:    "judging_lock_removed",
}
