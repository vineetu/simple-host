package mcp

// Imports family: WordPress imports and photo upload links.

func importTools() []Tool {
	return []Tool{
		{
			Name: "create_import_link", Title: "Move a WordPress site",
			Description: "Move a WordPress site here from the export the person downloads from WordPress themselves (WordPress.com: Tools > Export). Each call makes a new link. Returns a one-time page link (30 minutes, this site only) for them to open in their browser and drop the files on. The import creates the site, or publishes a new version of it, at its public address, so it can replace what is live (the previous version is kept for rollback_site). When they say it finished, call get_import_report. Never paste the link into a web page.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "theme_url": str("Optional old site's address, prefilled on the upload page to copy its look.")}, "site"),
			Annotations: writes(true, false, true), run: createImportLink,
		},
		{
			Name: "get_import_report", Title: "Read the WordPress import report",
			Description: "Read your import's progress, report and exact setup calls for its dynamic features. After it finishes, run those calls and check the pages.",
			InputSchema: object(map[string]any{"import_id": str("The import_id returned by create_import_link.")}, "import_id"),
			Annotations: readOnly(), run: getImportReport,
		},
		{
			Name:        "create_upload_link",
			Title:       "Photo picker: add the person's own photos",
			Description: "Opens a photo picker in the chat (apps that show MCP Apps); elsewhere returns photos_url to give the person. Call it as soon as a page needs the person's own photos (menu dishes, a listing, portfolio, team) and the site exists (publish the page first), even before they mention photos, instead of building an upload page or asking them to attach, convert, resize, or host photos. They pick any format on phone or computer; each is shrunk into images/, keeping other files. When they say done, get_site and place them. Photos visitors share are the gallery recipe instead. archive: true gives shell agents a whole-site replacement upload. Each call makes a new link.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "archive": map[string]any{"type": "boolean", "default": false, "description": "Shell archive upload that replaces the whole site."}}, "site"),
			Annotations: writes(true, false, true),
			run:         createUploadLink,
		},
	}
}
