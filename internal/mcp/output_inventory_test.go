package mcp

func outputSchemasWithLimits(limits Limits) map[string]map[string]any {
	schemas := map[string]map[string]any{}
	for _, tool := range toolsWithLimits(limits) {
		schemas[tool.Name] = tool.OutputSchema
	}
	return schemas
}

// Existing dispatch fixtures exercise the base event/entry schema declarations.
func hackOutputSchemas() map[string]map[string]any {
	schemas := map[string]map[string]any{}
	for _, tool := range append(hackBaseEventTools(), hackBaseEntryTools()...) {
		schemas[tool.Name] = tool.OutputSchema
	}
	return schemas
}

var hackRouteBodyDescriptions = func() map[string]string {
	descriptions := map[string]string{}
	for _, route := range hackOrganiserRoutes() {
		if route.bodyDescription != "" {
			descriptions[route.name] = route.bodyDescription
		}
	}
	return descriptions
}()
