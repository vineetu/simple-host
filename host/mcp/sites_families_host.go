package mcp

import "slices"

// hostFamilies returns the Simple Host tool families in registration order.
func hostFamilies(limits Limits) []familyTools {
	access := domainTools(limits)
	access = append(access, passcodeTools()...)
	access = append(access, secretTools()...)
	access = append(access, accessTools()...)
	storage := storageTools()
	storage = append(storage, visitorTools()...)
	storage = append(storage, storageVisitorEmailsTool())
	recipe := pageRecipeTool()
	recipe.Description += " After building a records form, offer notifications through MCP Events if the app supports them, or the dashboard Notifications panel."
	storage = append(storage, recipe)
	families := []familyTools{
		{Family: famAccounts, Tools: accountTools()},
		{Family: famSites, Tools: slices.Concat(siteReadTools(), siteDeployTools(limits), siteVersionTools(limits), siteLifecycleTools(limits), siteReportingTools(limits))},
		{Family: famImports, Tools: importTools()},
		{Family: famAccess, Tools: access},
		{Family: famStorage, Tools: storage},
	}
	return families
}

// Tools is the callable surface, in the order a first-time agent needs it,
// stated at the defaults. NewServer registers the same families with the
// instance's limits, optional tools and wording rewrites.
func Tools() []Tool { return ToolsWithLimits(DefaultLimits()) }

// ToolsWithLimits is Tools stated with limits instead of the defaults.
func ToolsWithLimits(limits Limits) []Tool {
	r := &toolRegistry{}
	r.addFamilies(editionHost, hostFamilies(limits), nil)
	return r.declaredTools(editionHost)
}
