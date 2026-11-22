package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func siteLifecycleTools(limits Limits) []Tool {
	return []Tool{
		{
			OutputSchema: outObject(map[string]any{
				"deleted":         outString("Name of the site that was deleted."),
				"restorable_days": outInteger("How many days restore_site can bring it back."),
			}, "deleted", "restorable_days"),
			Name:  "delete_site",
			Title: "Delete a site",
			Description: "DESTRUCTIVE: takes the site offline at once, with every version and all of its saved data (storage resources included). It stays in Recently deleted for " + span(limits.DeletedRetention) + ", where restore_site brings it back exactly as it was; after that it is gone for good. Its name stays taken until then. " +
				"Only call this after the person has explicitly confirmed, in this conversation, that they want this specific site deleted. Pass the site name twice: as `site` and as `confirm_name`.",
			InputSchema: object(map[string]any{
				"site":         str(siteDesc),
				"confirm_name": str("The same site name again, typed out, as confirmation."),
			}, "site", "confirm_name"),
			// Removes the public site and access to its data, even while restorable.
			Annotations: writes(true, true, true),
			run:         runDeleteSite,
		},
		{
			OutputSchema: outObject(map[string]any{
				"sites": outArray("Sites in Recently deleted, most recently deleted first.", outObject(map[string]any{
					"name":       outString(outSiteName),
					"deleted_at": outString("When it was deleted (RFC 3339)."),
					"purge_at":   outString("When it is removed for good (RFC 3339); restore_site works until then."),
				}, "name", "deleted_at", "purge_at")),
				"count": outInteger("How many sites are in Recently deleted."),
			}, "sites", "count"),
			Name:        "list_deleted_sites",
			Title:       "List recently deleted sites",
			Description: "List the account's sites in Recently deleted: each was deleted within the last " + span(limits.DeletedRetention) + " and can be brought back with restore_site until its purge_at time, when it is removed for good.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run:         runListDeletedSites,
		},
		{
			OutputSchema: withDescription(siteSummarySchema(), "The restored site, live again at its address."),
			Name:         "restore_site",
			Title:        "Restore a deleted site",
			Description:  "Bring back a site from Recently deleted (see list_deleted_sites): same name, same address, every version, saved data and connected address, live again at once.",
			InputSchema:  object(map[string]any{"site": str(siteDesc)}, "site"),
			// Serves the site again at its public address; nothing is lost.
			Annotations: writes(false, true, true),
			run:         runRestoreSite,
		},
		{
			OutputSchema: withDescription(siteSummarySchema(), "The site under its new name, at its new address."),
			Name:         "rename_site",
			Title:        "Rename a site",
			Description:  "Give a site a new name, which changes its address. Links to the old address keep working: they redirect to the new one until a new site is created with the old name. A connected custom domain stays attached. Not for \"a shorter link\" or \"an address without my username\": that is connect_domain with a free <name>.simple-host.app.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"new_name": str("The new site name: lowercase letters, numbers and hyphens."),
			}, "site", "new_name"),
			// Serves the site at a new public address. Nothing is deleted and
			// renaming back restores the old address.
			Annotations: writes(true, false, true),
			run:         runRenameSite,
		},
		{
			policy:       toolPolicy{},
			OutputSchema: outObject(map[string]any{"site": map[string]any{"type": []string{"string", "null"}}}, "site"),
			Name:         "set_home_page",
			Title:        "Choose my home page",
			Description:  "Make one of the person's sites their home page, served at their personal address https://<handle>.simple-host.app/ instead of the showcase (the list of their sites); site null brings the showcase back. The site's own address keeps working too. If that site goes offline or is deleted, the showcase shows again.",
			InputSchema:  object(map[string]any{"site": map[string]any{"type": []string{"string", "null"}, "description": "Your site's name, or null for the showcase."}}, "site"),
			Annotations:  writes(true, true, true),
			run:          runSetHomePage,
		},
		{
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"visibility": outEnum("List on my profile: public = on; unlisted = off. Every open site can be found by search engines.", "public", "unlisted"),
			}, "site", "visibility"),
			Name:        "set_visibility",
			Title:       "List on my profile",
			Description: "List on my profile: on shows the site on the owner's profile page (https://<handle>.simple-host.app/); off leaves it off. New sites start off. Every open site can be found by search engines. To keep a site out, use a passcode or named viewers, take it offline, or ship your own robots.txt / noindex.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"visibility": map[string]any{"type": "string", "enum": []string{"public", "unlisted"}, "description": "List on my profile: `public` = on; `unlisted` = off. Does not affect search engines."},
			}, "site", "visibility"),
			// Adds a site to, or removes it from, a public listing page.
			Annotations: writes(true, true, true),
			run:         runSetVisibility,
		},
		{
			OutputSchema: outObject(map[string]any{
				"site":    outString(outSiteName),
				"offline": outBool("Whether the site is offline now."),
				"url":     outString("The site's address (showing \"This site is offline\" while it is offline)."),
			}, "site", "offline", "url"),
			Name:  "set_site_offline",
			Title: "Take a site offline or back online",
			Description: "Take a site offline (`offline: true`): every address of it shows a plain \"This site is offline\" page and visitors can no longer save anything (RSVPs, votes, sign-ups, private lists), read its saved data or lists, or sign in, for example when an event is over or a form must stop taking entries. " +
				"Nothing is deleted: files, versions, saved data and lists are kept, and you can still update, read and export it. `offline: false` puts it back online at every address. Confirm with the person before taking a site offline.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"offline": map[string]any{"type": "boolean", "description": "true takes the site offline; false puts it back online."},
			}, "site", "offline"),
			// Changes what the public sees; nothing is deleted and the other
			// value undoes it.
			Annotations: writes(true, true, true),
			run:         runSetSiteOffline,
		},
		{
			OutputSchema: outObject(map[string]any{
				"site": outString(outSiteName),
				"keep": outBool("true: kept up for good, never removed as idle; false: moved to Recently deleted after " + span(limits.IdleAfter+limits.IdleGrace) + " without visits or updates."),
			}, "site", "keep"),
			Name:  "keep_site",
			Title: "Keep a site up for good",
			Description: "Mark a site Keep (or clear the mark). Simple Host moves a site nobody has visited or updated for " + span(limits.IdleAfter+limits.IdleGrace) + " to Recently deleted unless it is marked Keep. " +
				"Sites with their own domain or claimed name are never removed either.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"keep": map[string]any{"type": "boolean", "description": "true (default) keeps the site up for good; false lets it be removed again when idle."},
			}, "site"),
			// Changes a flag on the person's own site; reversible.
			Annotations: writes(true, false, true),
			run:         runKeepSite,
		},
	}
}

func runDeleteSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	confirm, err := stringArg(args, "confirm_name")
	if err != nil {
		return output{}, err
	}
	// The server refuses the delete unless confirm_name names the site.
	if err := c.doJSON("delete_site", http.MethodDelete, sitePath(name, "?confirm_name="+url.QueryEscape(confirm)), nil, nil); err != nil {
		return output{}, err
	}
	retention := c.limits().DeletedRetention
	text := "Deleted " + name + ". It is offline now and stays in Recently deleted for " + span(retention) + "; restore_site brings it back with all its versions and data."
	return output{Text: text, Structured: map[string]any{"deleted": name, "restorable_days": int(retention / (24 * time.Hour))}}, nil
}

func runListDeletedSites(c *call, _ map[string]any) (output, error) {
	var body struct {
		Sites []struct {
			Name      string `json:"name"`
			DeletedAt string `json:"deleted_at"`
			PurgeAt   string `json:"purge_at"`
		} `json:"sites"`
	}
	if err := c.doJSON("list_deleted_sites", http.MethodGet, "/v1/me/deleted-sites", nil, &body); err != nil {
		return output{}, err
	}
	sites := make([]any, 0, len(body.Sites))
	lines := make([]string, 0, len(body.Sites))
	for _, d := range body.Sites {
		sites = append(sites, map[string]any{"name": d.Name, "deleted_at": d.DeletedAt, "purge_at": d.PurgeAt})
		lines = append(lines, d.Name+" (restorable until "+d.PurgeAt+")")
	}
	text := "Nothing in Recently deleted."
	if len(lines) > 0 {
		text = "Recently deleted: " + strings.Join(lines, "; ")
	}
	return output{Text: text, Structured: map[string]any{"sites": sites, "count": len(sites)}}, nil
}

func runRestoreSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	var site restSite
	if err := c.doJSON("restore_site", http.MethodPost, sitePath(name, "/restore"), nil, &site); err != nil {
		return output{}, err
	}
	return output{Text: "Restored " + site.Name + ". Live again at " + site.liveURL(), Structured: site.summary()}, nil
}

func runRenameSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	newName, err := stringArg(args, "new_name")
	if err != nil {
		return output{}, err
	}
	body, _ := json.Marshal(map[string]string{"name": strings.ToLower(newName)})
	var site restSite
	if err := c.doJSON("rename_site", http.MethodPatch, sitePath(name, ""), body, &site); err != nil {
		return output{}, err
	}
	return output{Text: "Renamed to " + site.Name + ". New address: " + site.liveURL(), Structured: site.summary()}, nil
}

func runSetHomePage(c *call, args map[string]any) (output, error) {
	site, ok := args["site"]
	if !ok {
		return output{}, fmt.Errorf("site is required (name or null)")
	}
	if _, isName := site.(string); site != nil && !isName {
		return output{}, fmt.Errorf("site must be a name or null")
	}
	body, _ := json.Marshal(map[string]any{"site": site})
	var result map[string]any
	if err := c.doJSON("set_home_page", http.MethodPut, "/v1/me/home", body, &result); err != nil {
		return output{}, err
	}
	return output{Text: "Home page updated.", Structured: result}, nil
}

func runSetVisibility(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	vis, err := stringArg(args, "visibility")
	if err != nil {
		return output{}, err
	}
	body, _ := json.Marshal(map[string]string{"visibility": vis})
	if err := c.doJSON("set_visibility", http.MethodPut, sitePath(name, "/visibility"), body, nil); err != nil {
		return output{}, err
	}
	vis = strings.ToLower(vis)
	return output{Text: name + " is now " + vis + ".", Structured: map[string]any{"site": name, "visibility": vis}}, nil
}

func runSetSiteOffline(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	off, ok := args["offline"].(bool)
	if !ok {
		return output{}, errors.New("offline is required: true to take the site offline, false to put it back online")
	}
	body, _ := json.Marshal(map[string]bool{"offline": off})
	var site restSite
	if err := c.doJSON("set_site_offline", http.MethodPatch, sitePath(name, ""), body, &site); err != nil {
		return output{}, err
	}
	text := name + " is back online at " + site.liveURL()
	if site.Offline {
		text = name + " is offline: " + site.liveURL() + " shows \"This site is offline\" and visitor saves are refused. Nothing was deleted."
	}
	return output{Text: text, Structured: map[string]any{"site": name, "offline": site.Offline, "url": site.liveURL()}}, nil
}

func runKeepSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	keep, err := boolArg(args, "keep", true)
	if err != nil {
		return output{}, err
	}
	body, _ := json.Marshal(map[string]bool{"keep": keep})
	if err := c.doJSON("keep_site", http.MethodPut, sitePath(name, "/keep"), body, nil); err != nil {
		return output{}, err
	}
	text := name + " is marked Keep: it stays up even if nobody visits it."
	if !keep {
		text = name + " is no longer marked Keep: if nobody visits or updates it for " + span(c.limits().IdleAfter) + ", it moves to Recently deleted."
	}
	return output{Text: text, Structured: map[string]any{"site": name, "keep": keep}}, nil
}
