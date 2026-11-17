package mcp

import "maps"

const analyticsContextDescription = "Optional: why this call, in 15 to 25 words, third person. No names, emails, or secrets."

func (s *Server) offersAnalyticsContext(caller Caller) bool {
	return s.analytics != nil
}

func withAnalyticsContext(tool Tool) Tool {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if _, own := props["context"]; own {
		return tool
	}
	for _, composition := range []string{"oneOf", "anyOf", "allOf"} {
		if _, exists := tool.InputSchema[composition]; exists {
			return tool
		}
	}
	if kind, _ := tool.InputSchema["type"].(string); kind != "" && kind != "object" {
		return tool
	}
	tool.InputSchema = maps.Clone(tool.InputSchema)
	if tool.InputSchema == nil {
		tool.InputSchema = map[string]any{"type": "object"}
	}
	props = maps.Clone(props)
	if props == nil {
		props = map[string]any{}
	}
	props["context"] = map[string]any{"type": "string", "description": analyticsContextDescription}
	tool.InputSchema["properties"] = props
	return tool
}

func analyticsContextTools(tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		out[i] = withAnalyticsContext(tool)
	}
	return out
}

// Call validation always uses the original schema, including when analytics is off.
func takeAnalyticsContext(tool Tool, args map[string]any) (string, bool) {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if _, own := props["context"]; own {
		return "", false
	}
	intent, ok := args["context"].(string)
	delete(args, "context")
	return intent, ok
}
