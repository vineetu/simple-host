package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

// hackStorageTools is the page recipe under its Hack name; pages are published
// with the site and storage tools naming the event as owner.
func hackStorageTools() []mcp.Tool {
	recipe := hackPageRecipeTool()
	recipe.Name = "hack_get_page_recipe"
	return []mcp.Tool{recipe}
}
