package hackmcp

import (
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"maps"
	"strings"
)

func hackSiteCreateUploadLink(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Title = "Create a one-time archive upload link"
	tool.Description = mcp.ArchiveUploadDescription
	return tool
}

func hackSiteCreateSite(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.InputSchema = maps.Clone(tool.InputSchema)
	props := maps.Clone(tool.InputSchema["properties"].(map[string]any))
	tool.InputSchema["properties"] = props
	delete(props, "access")
	delete(props, "viewers")
	tool.Description = strings.Replace(tool.Description, "By default anyone with the returned URL can open it as soon as this returns; with access specific only the owner and named viewers can, from the first request. Ask the person who should open it.", "Anyone with the returned URL can open it as soon as this returns.", 1)
	tool.Description = strings.Replace(tool.Description, mcp.DeployPhotoTip, hackPhotoTip, 1)
	return tool
}

func hackSiteUpdateSite(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = strings.Replace(tool.Description, mcp.DeployPhotoTip, hackPhotoTip, 1)
	return tool
}

func hackSiteWhoAmI(mcp.Tool, mcp.Limits) mcp.Tool { return hackWhoAmITool() }

func hackSiteGetPageRecipe(mcp.Tool, mcp.Limits) mcp.Tool { return hackPageRecipeTool() }

func hackSiteSetVisibility(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = "Choose whether the team website appears on its account's public listing (public = listed, unlisted = not listed; new sites start unlisted). It does not affect search engines: every open site can be found by them. To keep it out, use a passcode or named viewers, take it offline, or ship your own robots.txt / noindex. It does not make the site or any storage resource private."
	return tool
}

func hackSiteDeleteSite(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = "DESTRUCTIVE: take this team's website offline with its published versions and storage. It stays in Recently deleted for " + mcp.Span(limits.DeletedRetention) + " and can be restored during that time. Only call this after the person has confirmed in this conversation that they want this team's website deleted. Pass the site name twice: as `site` and as `confirm_name`."
	return tool
}

func hackSiteRestoreSite(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = "Restore this team's website from Recently deleted, including its files, versions and storage, while the retention period lasts."
	return tool
}

func hackSiteSetSiteOffline(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = "Take this team's website offline or bring it back. Offline stops serving files and access to KV, SQLite and file resources; nothing is deleted. Ask before taking it offline."
	return tool
}

func hackSiteExportSite(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = "Make a private, expiring download link for a team's website archive. It contains the site files and its KV, SQLite and file storage. Share the link only with the site owner."
	base := tool
	return mcp.WithRun(tool, func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		out, err := mcp.CallTool(base, c, args)
		if err == nil {
			out.Text = "Private site archive link: " + fmt.Sprint(out.Structured["url"]) + ". It includes the site's saved data; keep the link private."
		}
		return out, err
	})
}

func hackSiteStorageGetUsage(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.Description = hackStorageUsageDescription
	return tool
}

// hackSiteStorageSqlExecute drops for, table and values from the schema:
// adding a row for one visitor is Simple Host's alone. A call from an older
// tool list may still send them; they are accepted and behave as before
// (table and values are ignored beside sql, and for is refused by the route).
func hackSiteStorageSqlExecute(tool mcp.Tool, limits mcp.Limits) mcp.Tool {
	tool.InputSchema = maps.Clone(tool.InputSchema)
	props := maps.Clone(tool.InputSchema["properties"].(map[string]any))
	delete(props, "for")
	delete(props, "table")
	delete(props, "values")
	tool.InputSchema["properties"] = props
	return mcp.AcceptUnlisted(tool, "for", "table", "values")
}
