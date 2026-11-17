package mcp

// Only the enabled get_site adds preview data. Keep its existing text and the
// shared site summary (including list_sites) unchanged.
func sitePreviewOutput(c *call, site restSite, data map[string]any) output {
	out := output{Text: jsonText(data), Structured: data}
	for _, screen := range c.server.enabledScreens(c.caller) {
		if screen.ID == "site-preview" {
			data["site_preview"] = map[string]any{
				"open_to_anyone": site.Access == "anyone" &&
					(site.Visibility == "public" || site.Visibility == "unlisted") &&
					!site.PasscodeProtected && !site.Offline && !site.Suspended && site.ActiveVersion > 0,
				"canonical_url": site.SiteURL,
			}
			break
		}
	}
	return out
}

func sitePreviewSchema(original map[string]any) map[string]any {
	schema, props := map[string]any{}, map[string]any{}
	for key, value := range original {
		schema[key] = value
	}
	for key, value := range original["properties"].(map[string]any) {
		props[key] = value
	}
	props["site_preview"] = outObject(map[string]any{
		"open_to_anyone": outBool("Whether the published site is open to anyone with its link."),
		"canonical_url":  outString("The site's platform address, before custom-domain or address-family preference."),
	}, "open_to_anyone", "canonical_url")
	schema["properties"] = props
	return schema
}
