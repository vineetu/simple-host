package mcp

// hostFamilies returns the Simple Host tool families in registration order.
func hostFamilies(limits Limits) []familyTools {
	access := domainTools(limits)
	access = append(access, passcodeTools()...)
	access = append(access, accessTools()...)
	storage := storageTools()
	storage = append(storage, visitorTools()...)
	storage = append(storage, storageVisitorEmailsTool())
	recipe := pageRecipeTool()
	recipe.Description += " After building a records form, offer notifications through MCP Events if the app supports them, or the dashboard Notifications panel."
	storage = append(storage, recipe)
	families := []familyTools{
		{family: famAccounts, tools: accountTools()},
		{family: famSites, tools: siteTools(limits)},
		{family: famImports, tools: importTools()},
		{family: famAccess, tools: access},
		{family: famStorage, tools: storage},
	}
	return families
}

// Tools is the callable surface, in the order a first-time agent needs it,
// stated at the defaults. NewServer registers the same families with the
// instance's limits, optional tools and wording rewrites.
func Tools() []Tool { return toolsWithLimits(DefaultLimits()) }

// ToolsWithLimits is Tools stated with l instead of the defaults, for a
// caller (a test) that wants the full surface's wording at a changed knob
// without building a whole Server.
func ToolsWithLimits(l Limits) []Tool { return toolsWithLimits(l) }

func toolsWithLimits(limits Limits) []Tool {
	r := &toolRegistry{}
	r.addFamilies(editionHost, hostFamilies(limits), nil)
	return r.declaredTools(editionHost)
}
