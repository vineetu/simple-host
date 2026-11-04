package mcp

import "sync"

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
// Declared alongside the tool in its family registrar.
type toolPolicy struct {
	memberResult, organiserResult, visitorResult, accessResult, passcodeResult string
	storageResource, visitorParams, pagedStorage                               bool
	judgeParameter, includeEmails, recordsPins, screenshot, optionalBody       bool

	hackSiteExcluded, personalExcluded, connectorHidden  bool
	notDeployOnly, unlisted, notSearchable               bool
	listWithPasscode                                     bool
	personalCredential, listWithoutTeam, preserveNumbers bool
	unwrap, discovery, notificationID                    bool
	screen                                               string
	showAllowed                                          hackAppOnlyAllowed
	flatList                                             func(Tool) Tool
	analytics                                            analyticsPolicy
	hackSite                                             func(Tool, Limits) Tool
	noSpamCheck                                          func(Tool) Tool
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
	feature string
}
type toolRegistry struct{ entries []toolEntry }

func (r *toolRegistry) add(edition toolEdition, family toolFamily, tools []Tool) {
	for _, tool := range tools {
		if edition == editionHack {
			tool.policy.analytics.emailIdentity = true
			tool.policy.analytics.contactEmail = true
			tool.policy.personalCredential = true
		}
		for _, entry := range r.entries {
			if entry.edition == edition && entry.tool.Name == tool.Name {
				panic("mcp: duplicate tool: " + tool.Name)
			}
		}
		r.entries = append(r.entries, toolEntry{tool: tool, family: family, edition: edition, policy: tool.policy, feature: string(family)})
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
			tool.policy = entry.policy
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

// withToolPolicy attaches policy to a declaration produced by an adapter.
func withToolPolicy(tool Tool, policy toolPolicy) Tool { tool.policy = policy; return tool }

// declarationRegistry derives name-only helper lookups from the same declarations
// used by each Server. It has Events enabled so optional notification tools exist.
// No backend method is called while assembling declarations.
type inventoryEvents struct{ EventsBackend }

var declarationOnce sync.Once
var declarations *toolRegistry

func declarationRegistry() *toolRegistry {
	declarationOnce.Do(func() {
		limits := DefaultLimits()
		declarations = newRegistry(Config{Limits: &limits, Events: inventoryEvents{}})
	})
	return declarations
}
func declaredPolicy(name string) toolPolicy {
	for _, entry := range declarationRegistry().entries {
		if entry.tool.Name == name {
			return entry.policy
		}
	}
	return toolPolicy{}
}

// InventoryTool identifies one registered declaration, including hidden tools.
type InventoryTool struct {
	Name, Family, Edition string
	Listed                bool
}

// Inventory returns every registered tool with Events enabled. Listed describes
// edition listing policy; caller-specific core and screen gates still apply.
func Inventory() []InventoryTool {
	s := NewServer(Config{Events: inventoryEvents{}})
	var out []InventoryTool
	for _, entry := range s.inventory() {
		edition := "host"
		if entry.edition == editionHack {
			edition = "hack"
		}
		if entry.edition == editionScreen {
			edition = "screen"
		}
		out = append(out, InventoryTool{entry.tool.Name, string(entry.family), edition, !entry.policy.unlisted && !entry.policy.connectorHidden})
	}
	return out
}
