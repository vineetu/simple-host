package mcp

// hackFamilies returns the Simple Hack tool families in registration order.
func hackFamilies() []familyTools {
	families := []familyTools{
		{Family: famHackEvents, Tools: append(hackEventTools(), hackChecklistTools()...)},
		{Family: famHackEntries, Tools: hackEntryTools()},
		{Family: famStorage, Tools: hackStorageTools()},
		{Family: famSites, Tools: hackWebTools()},
	}
	var base []Tool
	for _, family := range families {
		base = append(base, family.Tools...)
	}
	return append(families, familyTools{Family: famHackAppOnly, Tools: hackAppFamilyTools(base)})
}

// NewServer composes today's built-in product until D3-S1b supplies it from
// the composition root. An explicit zero contribution builds only Host tools.
func NewServer(cfg Config) *Server {
	if cfg.Contribution == nil {
		contribution := hackContribution()
		cfg.Contribution = &contribution
	}
	return newServer(cfg)
}

// Inventory returns today's composed documentation inventory with Events enabled.
func Inventory() []InventoryTool {
	limits := DefaultLimits()
	contribution := hackContribution()
	return inventoryTools(Config{Limits: &limits, Events: inventoryEvents{}, Contribution: &contribution})
}

func hackContribution() Contribution {
	return Contribution{
		Families: hackFamilies(), Notifications: hackNotificationTools(),
		SiteVariants: map[string]func(Tool, Limits) Tool{
			"create_site": hackSiteCreateSite, "update_site": hackSiteUpdateSite,
			"delete_site": hackSiteDeleteSite, "restore_site": hackSiteRestoreSite,
			"export_site": hackSiteExportSite, "set_site_offline": hackSiteSetSiteOffline,
			"set_visibility": hackSiteSetVisibility, "create_upload_link": hackSiteCreateUploadLink,
			"get_page_recipe": hackSiteGetPageRecipe, "storage_set_resource": hackSiteStorageSetResource,
			"storage_sql_schema": hackSiteStorageSqlSchema, "who_am_i": hackSiteWhoAmI,
		},
		Instructions: func(mode string) string {
			switch mode {
			case CallerModeEvents:
				return HackInstructions()
			case CallerModeHackSite:
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
		Apps: func(ctx AppContext) AppContribution {
			// The screens retain their existing methods until D3-S1c. This adapter
			// owns only immutable context; it never receives the serving Server.
			screens := &Server{cfg: Config{APIHost: ctx.APIHost}}
			if ctx.Fixed {
				screens.fixed = &inventory{}
			}
			return AppContribution{
				Screens: screens.hackEnabledScreens,
				Resources: func(c Caller, allowed bool) []map[string]any {
					out := []map[string]any{}
					for _, resource := range screens.hackAppResourcesWithMeta(c, allowed, ctx.Meta) {
						out = append(out, resource.(map[string]any))
					}
					return out
				},
				Gate: screens.hackAppsGate,
				Read: func(req Request, c Caller, allowed bool) (Response, bool) {
					return screens.readHackAppWithMeta(req, c, allowed, ctx.Meta), true
				},
			}
		},
	}
}

// newRegistry retains the built-in composition for existing in-package callers.
func newRegistry(cfg Config) *toolRegistry {
	if cfg.Contribution == nil {
		contribution := hackContribution()
		cfg.Contribution = &contribution
	}
	return buildRegistry(cfg)
}
