// famStorage registers event storage and the Hack page recipe.
package mcp

func hackStorageTools() []Tool {
	tools := eventStorageTools()
	// Organisers need the page recipes for their event website without
	// selecting a team; Hack tools are all hack_-prefixed.
	recipe := hackPageRecipeTool()
	recipe.Name = "hack_get_page_recipe"
	return append(tools, recipe)
}
