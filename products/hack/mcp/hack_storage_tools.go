// famStorage registers event storage and the Hack page recipe.
package hackmcp

import "github.com/vsriram/simple-host/host/mcp"

func hackStorageTools() []mcp.Tool {
	tools := eventStorageTools()
	// Organisers need the page recipes for their event website without
	// selecting a team; Hack tools are all hack_-prefixed.
	recipe := hackPageRecipeTool()
	recipe.Name = "hack_get_page_recipe"
	return append(tools, recipe)
}

func eventStorageTools() []mcp.Tool {
	return mcp.EventStorageTools(hackEventOwnerSentence, hackPhotoWording)
}
