package mcp

// hostFamilies returns the Simple Host tool families in registration order.
// TODO(4.4): split into one registrar per family.
func hostFamilies(limits Limits) []familyTools {
	return []familyTools{{family: famSites, tools: toolsWithLimits(limits)}}
}
