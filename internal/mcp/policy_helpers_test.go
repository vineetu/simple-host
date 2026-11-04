package mcp

// Existing name-based telemetry fixtures resolve policies from a local registry.
// Production callers pass their Server registry's policies directly.
func testPolicy(name string) toolPolicy {
	limits := DefaultLimits()
	s := &Server{registry: newRegistry(Config{Limits: &limits, Events: inventoryEvents{}})}
	return s.policy(name)
}
func analyticsParameters(name string, args map[string]any) any {
	return analyticsParametersForPolicy(testPolicy(name), args)
}
func analyticsResponse(name string, value any) any {
	return analyticsResponseForPolicy(testPolicy(name), value)
}
func analyticsSecretField(name, field string) bool {
	return analyticsSecretFieldForPolicy(testPolicy(name), field)
}
func dropAnalyticsValues(name string, value any) any {
	return dropAnalyticsValuesForPolicy(testPolicy(name), value)
}
