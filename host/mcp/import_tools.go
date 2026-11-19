package mcp

// Photo and archive upload links.

func importTools() []Tool {
	return []Tool{
		{
			policy: toolPolicy{screen: "photo-picker", flatList: flatUploadTool, analytics: analyticsPolicy{secretText: true, secretHow: true}},
			OutputSchema: outObject(map[string]any{
				"photos_url": outString("Link to give the person to add photos."),
				"folder":     outEnum("Photo folder.", "images/"),
				"next":       outString("What to tell the person and do after they finish."),
				"upload_url": outString("Private one-time archive upload address."),
				"method":     outEnum("HTTP method.", "PUT"),
				"expires_at": outString("Expiry in RFC 3339."),
				"how":        outString("Shell command and archive instructions."),
			}, "expires_at"),
			Name:        "create_upload_link",
			Title:       "Photo picker: add the person's own photos",
			Description: "Opens a photo picker in the chat (apps that show MCP Apps); elsewhere returns photos_url to give the person. Call it as soon as a page needs the person's own photos (menu dishes, a listing, portfolio, team) and the site exists (publish the page first), even before they mention photos, instead of building an upload page or asking them to attach, convert, resize, or host photos. They pick any format on phone or computer; each is shrunk into images/, keeping other files. When they say done, get_site and place them. Photos visitors share are the gallery recipe instead. archive: true gives shell agents a whole-site replacement upload. Each call makes a new link.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "archive": map[string]any{"type": "boolean", "default": false, "description": "Shell archive upload that replaces the whole site."}}, "site"),
			Annotations: writes(true, false, true),
			run:         createUploadLink,
		},
	}
}
