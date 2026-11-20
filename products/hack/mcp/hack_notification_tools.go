package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// Hack's notification tools are the platform's: the person's own
// subscriptions on a team site or the event website, named by site and owner
// (the event's name).
func hackNotificationTools() []mcp.Tool {
	tools := mcp.NotificationTools()
	for i := range tools {
		props := map[string]any{"site": mcp.Str("Your team's site, or the event website's (organiser.website_site); omit to list all your subscriptions."), "owner": mcp.Str("The event's name: the account that holds the site.")}
		required := []string{}
		if mcp.RequiresNotificationID(tools[i]) {
			props["id"] = mcp.Str("Subscription ID.")
			required = []string{"site", "id"}
		}
		tools[i].InputSchema = mcp.Object(props, required...)
	}
	tools[0].Description = "List your visitor-save subscriptions on a team site or the event website (site with owner), or all of them (neither)."
	return tools
}
