package mcp

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
	screenOnly, unwrap, discovery, notificationID        bool
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
	editionBrain
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
	r.addFamilies(editionHost, hostFamilies(*cfg.Limits), func(tools []Tool) []Tool { return instanceTools(addressTools(tools)) })
	if cfg.Events != nil {
		r.add(editionHost, famNotifications, instanceTools(addressTools(notificationTools())))
	}
	r.add(editionHost, famDiscovery, instanceTools(discoveryTools()))
	r.addFamilies(editionHack, hackFamilies(), addressTools)
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

// inventoryEvents marks Events enabled for the documentation inventory.
// Inventory assembly never calls backend methods.
type inventoryEvents struct{ EventsBackend }

// policy resolves name-only protocol inputs against this server's inventory.
func (s *Server) policy(name string) toolPolicy { return s.policies[name] }

// policyIndex is built once per Server. Host entries win when editions share
// a name (list_notifications/remove_notification), matching the old name-prefix
// behavior: those names have Host telemetry policy even in a Hack connection.
func (r *toolRegistry) policyIndex() map[string]toolPolicy {
	policies := make(map[string]toolPolicy, len(r.entries))
	for _, entry := range r.entries {
		if _, exists := policies[entry.tool.Name]; !exists || entry.edition == editionHost {
			policies[entry.tool.Name] = entry.policy
		}
	}
	return policies
}

// addFamilies shares registration and duplicate validation with the raw exports.
// A nil rewrite retains the declarations' original wording.
func (r *toolRegistry) addFamilies(edition toolEdition, families []familyTools, rewrite func([]Tool) []Tool) {
	for _, family := range families {
		tools := family.tools
		if rewrite != nil {
			tools = rewrite(tools)
		}
		r.add(edition, family.family, tools)
	}
}

// declaredTools is the unfiltered view used by the compatibility exports.
// Hidden declarations remain present, as in the original exports.
func (r *toolRegistry) declaredTools(edition toolEdition) []Tool {
	var tools []Tool
	for _, entry := range r.entries {
		if entry.edition == edition {
			tools = append(tools, entry.tool)
		}
	}
	return tools
}

// InventoryTool identifies one registered declaration, including hidden tools.
// Hidden tools are never offered to a connector; the rest are callable, and
// unlisted ones are reached through find_tools and run_tool.
type InventoryTool struct {
	Name, Family, Edition string
	Listed, Hidden        bool
	Title, Description    string
	InputSchema           map[string]any
}

// Inventory returns every registered tool with Events enabled. Listed describes
// edition listing policy; caller-specific core and screen gates still apply.
func Inventory() []InventoryTool {
	limits := DefaultLimits()
	r := newRegistry(Config{Limits: &limits, Events: inventoryEvents{}})
	var out []InventoryTool
	for _, entry := range r.entries {
		edition := "host"
		if entry.edition == editionHack {
			edition = "hack"
		}
		if entry.edition == editionScreen {
			edition = "screen"
		}
		out = append(out, InventoryTool{
			Name: entry.tool.Name, Family: string(entry.family), Edition: edition,
			Listed: !entry.policy.unlisted && !entry.policy.connectorHidden,
			Hidden: entry.policy.connectorHidden,
			Title:  entry.tool.Title, Description: entry.tool.Description, InputSchema: entry.tool.InputSchema,
		})
	}
	return out
}
