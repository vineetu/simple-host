package mcp

import (
	"fmt"
	"strings"
)

// toolFamily names one capability family of MCP tools (docs/architecture/
// target.md §6). Every registered tool belongs to exactly one family.
type toolFamily string

const (
	famSites         toolFamily = "sites"         // sites, deploys, versions
	famAccess        toolFamily = "access"        // passcodes, viewers, domains
	famStorage       toolFamily = "storage"       // current storage (KV, SQLite, files) and visitor tools
	famLegacy        toolFamily = "legacy"        // legacy Host storage
	famAccounts      toolFamily = "accounts"      // who am I, profile, connections
	famImports       toolFamily = "imports"       // imports and photo uploads
	famNotifications toolFamily = "notifications" // notifications and MCP Events
	famAdmin         toolFamily = "admin"         // admin and brain
	famDiscovery     toolFamily = "discovery"     // find_tools, run_tool
	famHackEvents    toolFamily = "hack-events"   // Hack events, members, teams
	famHackEntries   toolFamily = "hack-entries"  // Hack entries, judging, results
	famHackAppOnly   toolFamily = "hack-app-only" // Hack app-only screen tools
)

// familyTools is one family's tool declarations, in listing order.
type familyTools struct {
	family toolFamily
	tools  []Tool
}

// A basic build and the jobs people ask for most (SIM-187). 2026-10-27
// (SIM-226): AgentCat showed storage_list_resources, site_analytics and
// list_versions reached through run_tool more often than storage_get_kv,
// storage_put_file and storage_visitor_emails were called at all, so they
// swapped places. 2026-10-30 (SIM-190): a Haiku-only eval never found
// visitor_read_site through find_tools (it never called a tool at all in
// 3 of 5 runs), so it joins the core too.
var coreToolNames = []string{
	"who_am_i", "list_sites", "get_site", "read_site_file", "create_site", "update_site",
	"get_page_recipe", "create_upload_link", "visitor_read_site", "storage_set_resource",
	"storage_sql_schema", "storage_sql_query", "storage_sql_execute", "storage_put_kv",
	"storage_list_resources", "site_analytics", "list_versions", "set_site_access", "grant_site_viewer",
	"find_tools", "run_tool",
}

// toolPolicy is per-tool listing and edition policy, applied at registration.
// It can move onto Tool declarations without changing registry assembly.
type toolPolicy struct {
	hackSiteExcluded, personalExcluded, connectorHidden bool
	notDeployOnly, unlisted, notSearchable              bool
	listWithPasscode                                    bool
	hackSite                                            func(Tool, Limits) Tool
	noSpamCheck                                         func(Tool) Tool
}

var toolPolicies = map[string]toolPolicy{
	"visitor_read_site":    {hackSiteExcluded: true},
	"visitor_add_entry":    {hackSiteExcluded: true},
	"visitor_change_entry": {hackSiteExcluded: true},
	"list_notifications": {hackSiteExcluded: true,
		notDeployOnly: true},
	"remove_notification": {hackSiteExcluded: true,
		notDeployOnly: true},
	"create_import_link":     {hackSiteExcluded: true},
	"get_import_report":      {hackSiteExcluded: true},
	"set_keep_versions":      {hackSiteExcluded: true},
	"discard_version":        {hackSiteExcluded: true},
	"get_site_passcode":      {hackSiteExcluded: true, listWithPasscode: true},
	"set_site_passcode":      {hackSiteExcluded: true},
	"remove_site_passcode":   {hackSiteExcluded: true},
	"sign_out_site_visitors": {hackSiteExcluded: true},
	"set_site_access":        {hackSiteExcluded: true},
	"list_site_viewers":      {hackSiteExcluded: true},
	"grant_site_viewer":      {hackSiteExcluded: true},
	"revoke_site_viewer":     {hackSiteExcluded: true},
	"storage_visitor_emails": {hackSiteExcluded: true},
	"set_home_page":          {hackSiteExcluded: true},
	"set_bio":                {hackSiteExcluded: true},
	"set_showcase_site":      {hackSiteExcluded: true},
	"create_upload_link": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Title = "Create a one-time archive upload link"
		tool.Description = archiveUploadDescription
		return tool
	}},
	"create_site": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = strings.Replace(tool.Description, deployPhotoTip, hackPhotoTip, 1)
		return tool
	}},
	"update_site": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = strings.Replace(tool.Description, deployPhotoTip, hackPhotoTip, 1)
		return tool
	}},
	"who_am_i": {hackSite: func(tool Tool, limits Limits) Tool {
		tool = hackWhoAmITool()
		return tool
	}},
	"get_page_recipe": {personalExcluded: true,
		noSpamCheck: noSpamCheckRecipeTool,
		hackSite: func(tool Tool, limits Limits) Tool {
			tool = hackPageRecipeTool()
			return tool
		}},
	"storage_set_resource": {noSpamCheck: withoutSpamCheck,
		hackSite: func(tool Tool, limits Limits) Tool {
			tool.Description = hackPhotoWording(strings.Replace(tool.Description, storageOwnerSentence, hackTeamOwnerSentence, 1))
			tool = withoutSpamCheck(tool)
			return tool
		}},
	"storage_sql_schema": {noSpamCheck: withoutSpamCheck,
		hackSite: func(tool Tool, limits Limits) Tool {
			tool = withoutSpamCheck(tool)
			return tool
		}},
	"set_visibility": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = "Choose whether the team website appears on its account's public listing. Unlisted also keeps it out of search engines unless it has its own domain or name, and is still reachable by anyone with its address; it does not make the site or any storage resource private."
		return tool
	}},
	"delete_site": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = "DESTRUCTIVE: take this team's website offline with its published versions and storage. It stays in Recently deleted for " + span(limits.DeletedRetention) + " and can be restored during that time. Only call this after the person has confirmed in this conversation that they want this team's website deleted. Pass the site name twice: as `site` and as `confirm_name`."
		return tool
	}},
	"restore_site": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = "Restore this team's website from Recently deleted, including its files, versions and storage, while the retention period lasts."
		return tool
	}},
	"set_site_offline": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = "Take this team's website offline or bring it back. Offline stops serving files and access to KV, SQLite and file resources; nothing is deleted. Ask before taking it offline."
		return tool
	}},
	"export_site": {hackSite: func(tool Tool, limits Limits) Tool {
		tool.Description = "Make a private, expiring download link for a team's website archive. It contains site files and may include data saved before Simple Hack retired the old storage model. Current websites use KV, SQLite and files. Share the link only with the site owner."
		run := tool.run
		tool.run = func(c *call, args map[string]any) (output, error) {
			out, err := run(c, args)
			if err == nil {
				out.Text = "Private site archive link: " + fmt.Sprint(out.Structured["url"]) + ". It may include previously stored data; keep the link private."
			}
			return out, err
		}
		return tool
	}},
	// Minting a team key belongs in the trusted browser/REST workflow,
	// never in a connector result that could put the secret in chat.
	"hack_create_team_key": {connectorHidden: true},
	"find_tools": {unlisted: true,
		notSearchable: true},
	"run_tool": {unlisted: true,
		notSearchable: true},
}

type toolEdition uint8

const (
	editionHost toolEdition = iota
	editionHack
	editionScreen
)

type toolEntry struct {
	tool    Tool
	family  toolFamily
	edition toolEdition
	policy  toolPolicy
}
type toolRegistry struct{ entries []toolEntry }

func (r *toolRegistry) add(edition toolEdition, family toolFamily, tools []Tool) {
	for _, tool := range tools {
		for _, entry := range r.entries {
			if entry.edition == edition && entry.tool.Name == tool.Name {
				panic("mcp: duplicate tool: " + tool.Name)
			}
		}
		r.entries = append(r.entries, toolEntry{tool, family, edition, toolPolicies[tool.Name]})
	}
}

func newRegistry(cfg Config) *toolRegistry {
	r := &toolRegistry{}
	for _, family := range hostFamilies(*cfg.Limits) {
		r.add(editionHost, family.family, instanceTools(addressTools(family.tools)))
	}
	if cfg.Events != nil {
		r.add(editionHost, famNotifications, instanceTools(addressTools(notificationTools())))
	}
	r.add(editionHost, famDiscovery, instanceTools(discoveryTools()))
	for _, family := range hackFamilies() {
		r.add(editionHack, family.family, addressTools(family.tools))
	}
	if cfg.Events != nil {
		r.add(editionHack, famNotifications, hackNotificationTools())
	}
	r.add(editionScreen, famAccess, []Tool{passcodeScreenTool()})
	for i := range r.entries {
		entry := &r.entries[i]
		if entry.edition == editionHost && cfg.NoSpamCheck && entry.policy.noSpamCheck != nil {
			entry.tool = entry.policy.noSpamCheck(entry.tool)
		}
	}
	for _, name := range coreToolNames {
		found := false
		for _, entry := range r.entries {
			if entry.edition == editionHost && entry.tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			panic("mcp: missing core tool: " + name)
		}
	}
	return r
}

// inventory exposes registration order and family for documentation checks.
func (s *Server) inventory() []toolEntry {
	if s.registry == nil {
		return nil
	}
	return append([]toolEntry(nil), s.registry.entries...)
}

type registryView struct {
	tools, bare []Tool
	byName      map[string]Tool
}

func (r *toolRegistry) view(cfg Config, edition toolEdition, site, personal, deploy bool) registryView {
	v := registryView{byName: map[string]Tool{}}
	for _, entry := range r.entries {
		if entry.edition != edition || entry.policy.connectorHidden || (deploy && entry.policy.notDeployOnly) {
			continue
		}
		if site && (entry.policy.hackSiteExcluded || entry.policy.unlisted || (personal && entry.policy.personalExcluded)) {
			continue
		}
		tool := entry.tool
		if site && entry.policy.hackSite != nil {
			tool = entry.policy.hackSite(tool, *cfg.Limits)
		}
		v.byName[tool.Name] = tool
		if entry.policy.unlisted {
			continue
		}
		v.tools = append(v.tools, tool)
		tool.OutputSchema = nil
		v.bare = append(v.bare, tool)
	}
	return v
}
