package mcp

func outputSchemasWithLimits(limits Limits) map[string]map[string]any {
	schemas := map[string]map[string]any{}
	for _, tool := range ToolsWithLimits(limits) {
		schemas[tool.Name] = tool.OutputSchema
	}
	return schemas
}
