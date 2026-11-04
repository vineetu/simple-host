package mcp

// hackFamilies returns the Simple Hack tool families in registration order.
// TODO(4.4): split into one registrar per family.
func hackFamilies() []familyTools {
	return []familyTools{{family: famHackEvents, tools: HackTools()}}
}
