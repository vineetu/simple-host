// Hack-site variants are referenced by their Host declarations.
package mcp

import (
	"fmt"
	"strings"
)

func hackSiteCreateUploadLink(tool Tool, limits Limits) Tool {
	tool.Title = "Create a one-time archive upload link"
	tool.Description = archiveUploadDescription
	return tool
}

func hackSiteCreateSite(tool Tool, limits Limits) Tool {
	tool.Description = strings.Replace(tool.Description, deployPhotoTip, hackPhotoTip, 1)
	return tool
}

func hackSiteUpdateSite(tool Tool, limits Limits) Tool {
	tool.Description = strings.Replace(tool.Description, deployPhotoTip, hackPhotoTip, 1)
	return tool
}

func hackSiteWhoAmI(tool Tool, limits Limits) Tool {
	tool = hackWhoAmITool()
	return tool
}

func hackSiteGetPageRecipe(tool Tool, limits Limits) Tool {
	tool = hackPageRecipeTool()
	return tool
}

func hackSiteStorageSetResource(tool Tool, limits Limits) Tool {
	tool.Description = hackPhotoWording(strings.Replace(tool.Description, storageOwnerSentence, hackTeamOwnerSentence, 1))
	tool = withoutSpamCheck(tool)
	return tool
}

func hackSiteStorageSqlSchema(tool Tool, limits Limits) Tool {
	tool = withoutSpamCheck(tool)
	return tool
}

func hackSiteSetVisibility(tool Tool, limits Limits) Tool {
	tool.Description = "Choose whether the team website appears on its account's public listing (public = listed, unlisted = not listed; new sites start unlisted). It does not affect search engines: every open site can be found by them. To keep it out, use a passcode or named viewers, take it offline, or ship your own robots.txt / noindex. It does not make the site or any storage resource private."
	return tool
}

func hackSiteDeleteSite(tool Tool, limits Limits) Tool {
	tool.Description = "DESTRUCTIVE: take this team's website offline with its published versions and storage. It stays in Recently deleted for " + span(limits.DeletedRetention) + " and can be restored during that time. Only call this after the person has confirmed in this conversation that they want this team's website deleted. Pass the site name twice: as `site` and as `confirm_name`."
	return tool
}

func hackSiteRestoreSite(tool Tool, limits Limits) Tool {
	tool.Description = "Restore this team's website from Recently deleted, including its files, versions and storage, while the retention period lasts."
	return tool
}

func hackSiteSetSiteOffline(tool Tool, limits Limits) Tool {
	tool.Description = "Take this team's website offline or bring it back. Offline stops serving files and access to KV, SQLite and file resources; nothing is deleted. Ask before taking it offline."
	return tool
}

func hackSiteExportSite(tool Tool, limits Limits) Tool {
	tool.Description = "Make a private, expiring download link for a team's website archive. It contains site files and may include data saved before Simple Hack retired the old storage model. Current websites use KV, SQLite and files. Share the link only with the site owner."
	run := tool.run
	tool.run = func(c *call, args map[string]any) (output, error) {
		out, err := run(c, args)
		if err == nil {
			out.Text = "Private site archive link: " + fmt.Sprint(out.Structured["url"]) + ". It may include previously stored data; keep the link private."
		}
		return out, err
	}
	return tool
}
