package mcp

// Hack uses the personal credential for these tools, as for event storage.
func hackNotificationTools() []Tool {
	tools := notificationTools()
	for i := range tools {
		props := map[string]any{"site": str("Selected team site; omit when using event."), "event": str("Event slug for the organiser website, as in hack_event_storage tools.")}
		required := []string{}
		if tools[i].policy.notificationID {
			props["id"] = str("Subscription ID.")
			required = []string{"id"}
		}
		tools[i].InputSchema = object(props, required...)
	}
	props := tools[0].OutputSchema["properties"].(map[string]any)
	items := props["notifications"].(map[string]any)["items"].(map[string]any)
	items["properties"].(map[string]any)["event"] = outString("Organiser website event slug. Use event instead of site to remove its subscription.")
	tools[0].Description = "List your visitor-save subscriptions for a selected team site or an organiser website. Omit both to list your subscriptions."
	return tools
}
