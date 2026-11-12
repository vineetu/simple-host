package mcp

// Imports family: WordPress imports and photo upload links.

func importTools() []Tool {
	return []Tool{
		{
			policy: toolPolicy{analytics: analyticsPolicy{secretText: true}, hackSiteExcluded: true},
			OutputSchema: outObject(map[string]any{
				"import_url": outString("Private one-time browser upload page."),
				"import_id":  outString("Import report identifier."),
				"expires_at": outString("Expiry in RFC 3339."),
				"site":       outString(outSiteName),
				"how":        outString("Instructions for the person and chat."),
			}, "import_url", "import_id", "expires_at", "site", "how"),
			Name: "create_import_link", Title: "Move a WordPress site",
			Description: "Move a WordPress site here from the export the person downloads from WordPress themselves (WordPress.com: Tools > Export). Each call makes a new link. Returns a one-time page link (30 minutes, this site only) for them to open in their browser and drop the files on. The import creates the site, or publishes a new version of it, at its public address, so it can replace what is live (the previous version is kept for rollback_site). When they say it finished, call get_import_report. Never paste the link into a web page.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "theme_url": str("Optional old site's address, prefilled on the upload page to copy its look.")}, "site"),
			Annotations: writes(true, false, true), run: createImportLink,
		},
		{
			policy: toolPolicy{hackSiteExcluded: true},
			OutputSchema: outObject(map[string]any{
				"state":       outString("Import state."),
				"message":     outString("Plain progress or failure."),
				"site":        outString(outSiteName),
				"site_url":    outString("Live address."),
				"started_at":  outString("Start in RFC 3339, or empty."),
				"finished_at": outString("Finish in RFC 3339, or empty."),
				"report":      map[string]any{"type": "object", "additionalProperties": true},
				"next_steps":  outArray("Feature setup steps and live address handoff.", outString("Step.")),
			}, "state", "message", "site", "site_url", "started_at", "finished_at", "next_steps"),
			Name: "get_import_report", Title: "Read the WordPress import report",
			Description: "Read your import's progress, report and exact setup calls for its dynamic features. After it finishes, run those calls and check the pages. Write root _redirects rules from the old address list for any changed paths (including wildcards and ?p=123 query links); fix publish errors at their line numbers.",
			InputSchema: object(map[string]any{"import_id": str("The import_id returned by create_import_link.")}, "import_id"),
			Annotations: readOnly(), run: getImportReport,
		},
		{
			policy: toolPolicy{screen: "photo-picker", flatList: flatUploadTool, analytics: analyticsPolicy{secretText: true, secretHow: true}, hackSite: hackSiteCreateUploadLink},
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
