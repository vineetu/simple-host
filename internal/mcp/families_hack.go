package mcp

// hackFamilies returns the Simple Hack tool families in registration order.
func hackFamilies() []familyTools {
	families := []familyTools{
		{family: famHackEvents, tools: append(hackEventTools(), hackChecklistTools()...)},
		{family: famHackEntries, tools: hackEntryTools()},
		{family: famStorage, tools: hackStorageTools()},
		{family: famSites, tools: hackWebTools()},
	}
	var base []Tool
	for _, family := range families {
		base = append(base, family.tools...)
	}
	return append(families, familyTools{family: famHackAppOnly, tools: hackAppFamilyTools(base)})
}
