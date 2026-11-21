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
		HostTools:     hostTools,
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
		Apps: hackApps, Discovery: hackDiscovery(),
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

// hostTools are the Simple Host tools a Simple Hack connection also serves,
// acting on a team's or event's site. A new Host tool is not served here
// until it is added to this list. When you add one, also add it to
// plugins/openai-hack/chatgpt-app-submission.json.
var hostTools = []string{
	"who_am_i", "list_sites", "get_site", "read_site_file", "create_site", "update_site",
	"create_upload_link", "list_versions", "preview_version", "rollback_site",
	"rename_site", "delete_site", "restore_site", "list_deleted_sites", "keep_site",
	"export_site", "set_site_offline", "set_visibility", "site_analytics",
	"connect_domain", "domain_status", "remove_domain",
	"storage_list_resources", "storage_set_resource", "storage_delete_resource", "storage_get_usage",
	"storage_get_kv", "storage_put_kv", "storage_delete_kv", "storage_list_kv_keys",
	"storage_sql_query", "storage_sql_execute", "storage_sql_schema",
	"storage_put_file", "storage_delete_file", "storage_list_file_objects", "storage_file_download_link",
}
