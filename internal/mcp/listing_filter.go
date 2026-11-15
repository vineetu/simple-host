package mcp

// showToolsAllowed evaluates each declaration's listing predicate against this
// connection. Fixed-inventory servers never offer product screens.
func (s *Server) showToolsAllowed(c Caller, uiCapable bool) map[string]bool {
	allowed := map[string]bool{}
	for _, entry := range s.inventory() {
		if entry.policy.showAllowed != nil {
			allowed[entry.tool.Name] = s.fixed == nil && entry.policy.showAllowed(c, uiCapable)
		}
	}
	return allowed
}

// filterShowTools hides only declarations with a false listing predicate.
func filterShowTools(list []Tool, allowed map[string]bool) []Tool {
	out := make([]Tool, 0, len(list))
	for _, tool := range list {
		if ok, named := allowed[tool.Name]; named && !ok {
			continue
		}
		out = append(out, tool)
	}
	return out
}
