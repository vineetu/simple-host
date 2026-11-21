package mcp

import "slices"

// toolFamily names one capability family of MCP tools (docs/architecture/
// target.md §6). Every registered tool belongs to exactly one family. A
// product names its own families in its contribution.
type toolFamily string

const (
	famSites         toolFamily = "sites"         // sites, deploys, versions
	famAccess        toolFamily = "access"        // passcodes, viewers, domains
	famStorage       toolFamily = "storage"       // current storage (KV, SQLite, files) and visitor tools
	famAccounts      toolFamily = "accounts"      // who am I, profile, connections
	famImports       toolFamily = "imports"       // archive and photo uploads
	famNotifications toolFamily = "notifications" // notifications and MCP Events
	famDiscovery     toolFamily = "discovery"     // find_tools, run_tool
)

// familyTools is one family's tool declarations, in listing order.
type familyTools struct {
	Family toolFamily
	Tools  []Tool
}

// The core list: a basic build and the jobs people ask for most, chosen from
// usage analytics. Every other tool is reached through find_tools.
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
	visitorParams, pagedStorage                                                bool
	judgeParameter, includeEmails, recordsPins, screenshot, optionalBody       bool

	connectorHidden, notDeployOnly, unlisted, notSearchable bool
	listWithPasscode                                        bool
	preserveNumbers                                         bool
	screenOnly, unwrap, discovery, notificationID           bool
	screen                                                  string
	showAllowed                                             func(Caller, bool) bool
	flatList                                                func(Tool) Tool
	analytics                                               analyticsPolicy
	noSpamCheck                                             func(Tool) Tool
	// unlistedArgs are accepted although the schema no longer lists them,
	// for apps that keep an older tool list.
	unlistedArgs []string
}

type toolEdition uint8

const (
	editionHost toolEdition = iota
	editionProduct
	editionScreen
)

type toolEntry struct {
	tool    Tool
	family  toolFamily
	edition toolEdition
}
type toolRegistry struct{ entries []toolEntry }

func (r *toolRegistry) add(edition toolEdition, family toolFamily, tools []Tool) {
	for _, tool := range tools {
		if edition == editionProduct {
			tool.policy.analytics.emailIdentity = true
			tool.policy.analytics.contactEmail = true
			tool.policy.analytics.product = true
		}
		for _, entry := range r.entries {
			if entry.edition == edition && entry.tool.Name == tool.Name {
				panic("mcp: duplicate tool: " + tool.Name)
			}
		}
		r.entries = append(r.entries, toolEntry{tool: tool, family: family, edition: edition})
	}
}

// buildRegistry registers every declaration. appFamily, when not nil, is the
// product's AppContribution.AppFamily for this server: its family follows the
// product's own.
func buildRegistry(cfg Config, appFamily func(base []Tool) FamilyTools) *toolRegistry {
	if cfg.Contribution == nil {
		cfg.Contribution = &Contribution{}
	}
	r := &toolRegistry{}
	r.addFamilies(editionHost, hostFamilies(*cfg.Limits), func(tools []Tool) []Tool { return ownerTools(instanceTools(addressTools(tools))) })
	if cfg.Teams {
		r.add(editionHost, famAccounts, instanceTools(addressTools(teamTools())))
		r.add(editionHost, famAccounts, ownerTools(instanceTools(addressTools(editorTools()))))
	}
	if cfg.Events != nil {
		r.add(editionHost, famNotifications, ownerTools(instanceTools(addressTools(notificationTools()))))
	}
	r.add(editionHost, famDiscovery, instanceTools(discoveryTools()))
	families := cfg.Contribution.Families
	if appFamily != nil {
		var base []Tool
		for _, family := range families {
			base = append(base, family.Tools...)
		}
		families = append(append([]familyTools(nil), families...), appFamily(base))
	}
	r.addFamilies(editionProduct, families, addressTools)
	if cfg.Events != nil {
		r.add(editionProduct, famNotifications, cfg.Contribution.Notifications)
	}
	r.add(editionScreen, famAccess, []Tool{passcodeScreenTool()})
	for i := range r.entries {
		entry := &r.entries[i]
		if entry.edition == editionHost && cfg.NoSpamCheck && entry.tool.policy.noSpamCheck != nil {
			policy := entry.tool.policy
			entry.tool = policy.noSpamCheck(entry.tool)
			entry.tool.policy = policy
		}
	}
	for _, name := range cfg.Contribution.HostTools {
		if !slices.ContainsFunc(r.entries, func(e toolEntry) bool { return e.edition == editionHost && e.tool.Name == name }) {
			panic("mcp: Contribution.HostTools names " + name + ", which is not a Host tool")
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

// view is one edition's tools. site is a product connection's view of the
// Host tools: only those the product names in Contribution.HostTools.
func (r *toolRegistry) view(cfg Config, edition toolEdition, site, deploy bool) registryView {
	v := registryView{byName: map[string]Tool{}}
	for _, entry := range r.entries {
		tool, p := entry.tool, entry.tool.policy
		if entry.edition != edition || p.connectorHidden || (deploy && p.notDeployOnly) {
			continue
		}
		if site && (p.unlisted || !slices.Contains(cfg.Contribution.HostTools, tool.Name)) {
			continue
		}
		if site && cfg.Contribution != nil && cfg.Contribution.SiteVariants[tool.Name] != nil {
			variant := cfg.Contribution.SiteVariants[tool.Name]
			tool = variant(tool, *cfg.Limits)
			// A variant changes the schema, not the policy; only the
			// arguments it still accepts unlisted carry over.
			unlisted := tool.policy.unlistedArgs
			tool.policy = p
			tool.policy.unlistedArgs = unlisted
		}
		if site && cfg.Contribution != nil && cfg.Contribution.OwnerArg != "" {
			tool = withOwnerArg(tool, cfg.Contribution.OwnerArg)
		}
		v.byName[tool.Name] = tool
		if p.unlisted {
			continue
		}
		v.tools = append(v.tools, tool)
		tool.OutputSchema = nil
		v.bare = append(v.bare, tool)
	}
	return v
}

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
			policies[entry.tool.Name] = entry.tool.policy
		}
	}
	return policies
}

// addFamilies shares registration and duplicate validation with the raw exports.
// A nil rewrite retains the declarations' original wording.
func (r *toolRegistry) addFamilies(edition toolEdition, families []familyTools, rewrite func([]Tool) []Tool) {
	for _, family := range families {
		tools := family.Tools
		if rewrite != nil {
			tools = rewrite(tools)
		}
		r.add(edition, family.Family, tools)
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
	Annotations           map[string]any
}

// Inventory returns every registered tool with Events enabled. Listed describes
// edition listing policy; caller-specific core and screen gates still apply.
func inventoryTools(cfg Config, appFamily func(base []Tool) FamilyTools) []InventoryTool {
	r := buildRegistry(cfg, appFamily)
	var out []InventoryTool
	for _, entry := range r.entries {
		edition := "host"
		if entry.edition == editionProduct {
			edition = "product"
		}
		if entry.edition == editionScreen {
			edition = "screen"
		}
		out = append(out, InventoryTool{
			Name: entry.tool.Name, Family: string(entry.family), Edition: edition,
			Listed: !entry.tool.policy.unlisted && !entry.tool.policy.connectorHidden,
			Hidden: entry.tool.policy.connectorHidden,
			Title:  entry.tool.Title, Description: entry.tool.Description, InputSchema: entry.tool.InputSchema,
			Annotations: entry.tool.Annotations,
		})
	}
	return out
}
