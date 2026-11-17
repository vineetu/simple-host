package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// Hack uses the personal credential for these tools, as for event storage.
func hackNotificationTools() []mcp.Tool {
	tools := mcp.NotificationTools()
	for i := range tools {
		props := map[string]any{"site": mcp.Str("Selected team site; omit when using event."), "event": mcp.Str("Event slug for the organiser website, as in hack_event_storage tools.")}
		required := []string{}
		if mcp.RequiresNotificationID(tools[i]) {
			props["id"] = mcp.Str("Subscription ID.")
			required = []string{"id"}
		}
		tools[i].InputSchema = mcp.Object(props, required...)
	}
	props := tools[0].OutputSchema["properties"].(map[string]any)
	items := props["notifications"].(map[string]any)["items"].(map[string]any)
	items["properties"].(map[string]any)["event"] = mcp.OutString("Organiser website event slug. Use event instead of site to remove its subscription.")
	tools[0].Description = "List your visitor-save subscriptions for a selected team site or an organiser website. Omit both to list your subscriptions."
	return tools
}
