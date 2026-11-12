package mcp

// Sites family: publishing, versions, visibility and analytics.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func siteTools(limits Limits) []Tool {
	return []Tool{
		{
			OutputSchema: outObject(map[string]any{
				"sites": outArray("Sites returned by the requested filters and page.", siteSummaryWith(map[string]any{
					"offline": outBool("Present (true) only when the owner has taken the site offline: every address shows \"This site is offline\" and visitor saves are refused."),
				})),
				"count": outInteger("Number of sites returned."),
			}, "sites", "count"),
			policy:      toolPolicy{listWithoutTeam: true, screen: "my-sites"},
			Name:        "list_sites",
			Title:       "List my sites",
			Description: "List this account's own sites (admins: pass all_accounts for every account's), newest edited first. Call this before changing a site to get its exact name. Optionally filter with query, dates, order, and limit/offset for paging.",
			InputSchema: object(map[string]any{
				"query":          str("Words to find in the site's name, domain, title, description, headings or text; every word must match."),
				"created_after":  str("Created on or after RFC3339 or YYYY-MM-DD (UTC)."),
				"created_before": str("Created on or before RFC3339 or YYYY-MM-DD (whole UTC day)."),
				"updated_after":  str("Updated on or after RFC3339 or YYYY-MM-DD (UTC)."),
				"updated_before": str("Updated on or before RFC3339 or YYYY-MM-DD (whole UTC day)."),
				"order":          map[string]any{"type": "string", "enum": []string{"updated", "created", "name"}, "description": "Sort key; default updated (last edited first)."},
				"limit":          map[string]any{"type": "integer", "description": "Maximum results; default 200 with filters, capped at 500."},
				"offset":         map[string]any{"type": "integer", "description": "Results to skip; default 0."},
				"all_accounts":   map[string]any{"type": "boolean", "description": "Admins only: every account's sites instead of just this one."},
			}),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				v := url.Values{}
				for _, key := range []string{"query", "created_after", "created_before", "updated_after", "updated_before", "order", "limit", "offset"} {
					if value, ok := args[key]; ok {
						if number, ok := value.(float64); ok {
							v.Set(key, strconv.FormatFloat(number, 'f', -1, 64))
						} else {
							v.Set(key, fmt.Sprint(value))
						}
					}
				}
				if all, ok := args["all_accounts"].(bool); ok && all {
					v.Set("all", "1")
				}
				var sites []restSite
				var err error
				if len(v) == 0 {
					sites, err = c.listSites()
				} else {
					res := c.do(http.MethodGet, "/v1/sites?"+v.Encode(), nil, nil)
					if !res.ok() {
						return output{}, restError("list_sites", res)
					}
					if err := json.Unmarshal(res.body, &sites); err != nil {
						return output{}, errors.New("list_sites: unexpected answer from the server")
					}
				}
				if err != nil {
					return output{}, err
				}

				items := make([]any, 0, len(sites))
				for _, s := range sites {
					item := s.summary()
					if s.Offline {
						item["offline"] = true
					}
					items = append(items, item)
				}
				out := map[string]any{"sites": items, "count": len(items)}
				if len(items) == 0 {
					if len(v) > 0 {
						return output{Text: "No sites match these filters.", Structured: out}, nil
					}

					return output{Text: "This account has no sites yet. Publish one with create_site.", Structured: out}, nil
				}
				text := jsonText(out)
				// A Hack team connection is the team's, never marked, so the
				// "not counting your own visits" line would not be true there.
				if views := ownerViewsLine(sites); views != "" && c.caller.Mode != CallerModeHackSite {
					text += "\n\n" + views
				}
				if c.server.mySitesEnabled(c.caller) {
					for i, s := range sites {
						items[i].(map[string]any)["display"] = map[string]any{
							"access": s.Access, "passcode_protected": s.PasscodeProtected,
							"updated_at": s.UpdatedAt, "canonical_url": s.SiteURL,
						}
					}
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			OutputSchema: siteSummaryWith(map[string]any{
				"files": outArray("Files in the live version. Absent when nothing is published yet.", outObject(map[string]any{
					"path": outString("Path from the site root, e.g. `css/style.css`."),
					"size": outInteger("Size in bytes."),
				}, "path", "size")),
			}),
			policy: toolPolicy{screen: "site-preview"},
			Name:   "get_site",
			Title:  "Get one site and its files",
			Description: "Show one site: its live address, active version, custom domain, and the list of files in the live version with their sizes. " +
				"Call this before editing an existing site, then read_site_file for each file you will change. It names only the live version; list_versions shows every kept version.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				site, err := c.findSite(name)
				if err != nil {
					return output{}, err
				}
				out := site.summary()
				if site.ActiveVersion > 0 {
					res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(site.ActiveVersion)+"/files", nil, nil)
					if res.ok() {
						var listing struct {
							Files []struct {
								Path string `json:"path"`
								Size int64  `json:"size"`
							} `json:"files"`
						}
						_ = json.Unmarshal(res.body, &listing)
						files := make([]any, 0, len(listing.Files))
						for _, f := range listing.Files {
							files = append(files, map[string]any{"path": f.Path, "size": f.Size})
						}
						out["files"] = files
					}
				}
				answer := sitePreviewOutput(c, site, out)
				if site.Views != nil && c.caller.Mode != CallerModeHackSite {
					answer.Text += "\n\nViews by people, not counting your own visits: " + strconv.FormatInt(site.Views.People7Days, 10) +
						" in the last 7 days, " + strconv.FormatInt(site.Views.People30Days, 10) + " in the last 30 days." + ownVisitsCaveat
				}
				return answer, nil
			},
		},
		{
			OutputSchema: outObject(map[string]any{
				"site": outString(outSiteName),
				"files": map[string]any{"type": "array", "description": "With paths: each file, in the order asked.", "items": outObject(map[string]any{
					"path":      outString("The file's path, as asked for."),
					"size":      outInteger("The file's full size in bytes."),
					"content":   outString("The file's text. Present for text files only."),
					"truncated": outBool("True when content holds only the first 200 KB."),
					"binary":    outBool("True for a binary file, whose content is not returned."),
					"error":     outString("Why this file could not be read. Present only then."),
				}, "path")},
				"path":      outString("The file's path, as asked for."),
				"version":   outInteger("The version the file was read from."),
				"size":      outInteger("The file's full size in bytes."),
				"content":   outString("The file's text. Present for text files only."),
				"truncated": outBool("True when content holds only the first 200 KB of a larger file. Present only then."),
				"binary":    outBool("True for a binary file (image, font, audio, video), whose content is not returned. Present only then."),
			}, "site", "version"),
			policy: toolPolicy{analytics: analyticsPolicy{fileResult: true, fileContentSize: true}},
			Name:   "read_site_file",
			Title:  "Read files from a site",
			Description: "Return the contents of one file of a site (path), or of up to 10 at once (paths), from the live version unless `version` is given. Read only the files you will change; for a small change you can skip reading and use update_site's edits. " +
				"Text files come back as text; binary files (images, fonts) are only described. Use update_site with keep_other_files: true to keep files you are not changing.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"path":    str("Path of the file from the site root, e.g. `index.html` or `css/style.css`."),
				"paths":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": maxReadPaths, "description": "Several paths read in one call, instead of path."},
				"version": map[string]any{"type": "integer", "description": "Version number to read from. Defaults to the live version."},
			}, "site"),
			Annotations: readOnly(),
			run:         readSiteFiles,
		},
		{
			policy: toolPolicy{flatList: flatDeployTool, analytics: analyticsPolicy{fileArguments: true}, hackSite: hackSiteCreateSite},
			OutputSchema: func() map[string]any {
				schema := siteSummaryWith(map[string]any{
					"file_count":    outInteger("How many files were published."),
					"page_warnings": pageWarningsSchema,
				}, "file_count")
				props := schema["properties"].(map[string]any)
				delete(props, "custom_domain")
				delete(props, "domain_status")
				return schema
			}(),
			Name:  "create_site",
			Title: "Publish a new site",
			Description: "Publish a NEW website from files given inline, at a public address. Fails if this account already has a site of that name, so it never overwrites anything; to change an existing site use update_site. " +
				"When the person says \"my site\", \"our site\", or \"my salon site\", call list_sites first: if they already have that site, add to it with update_site instead of making a new one. " +
				"`index.html` is required. Use relative links only (`css/style.css`, never `/css/style.css`), because a site can be served under a path as well as at a root. " +
				"The site is public to anyone with the returned URL as soon as this returns. A root _redirects file holds redirect rules, is validated on publish and is never served. " + deployFormTip + deploySizeTips,
			InputSchema: object(map[string]any{
				"site":         str(siteDesc + " Pick a short, descriptive name."),
				"files":        filesSchema(),
				"files_base64": filesBase64Schema(),
			}, "site", "files"),
			// Publishes to the public web; creates only, so nothing is lost.
			Annotations: writes(false, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "create") },
		},
		{
			policy: toolPolicy{flatList: flatDeployTool, analytics: analyticsPolicy{secretText: true, fileArguments: true, edits: true}, hackSite: hackSiteUpdateSite},
			OutputSchema: siteSummaryWith(map[string]any{
				"file_count":          outInteger("How many files the new version has."),
				"page_warnings":       pageWarningsSchema,
				"unpublished_version": outInteger("Only with publish: false: the version stored without going live (active_version is still the live one)."),
				"preview_url":         outString("Only with publish: false: a link showing the stored version that works for anyone who has it, for " + span(limits.PreviewLinkTTL) + ". Give it to the person; do not post it publicly."),
			}, "file_count"),
			Name:  "update_site",
			Title: "Publish a new version of a site",
			Description: "Replace the live files of an EXISTING site with a new version, at its public address. By default the files you send are the complete new version. To change one or two pages of a big site, send just those files with keep_other_files: true. remove_files lists paths to drop, only when keeping other files. " +
				"For a small change (hours, a price, a line of text), send edits instead: each {path, find, replace} replaces text that occurs exactly once in the live file; if a find is missing or repeated, nothing is published. " +
				"`index.html` is required unless keeping other files; use relative links only. The previous version is kept and can be made live again with rollback_site. Fails if there is no site of that name (use create_site). " +
				"A root _redirects file holds redirect rules, is validated on publish and is never served. " + deployFormTip + deploySizeTips + " With `publish: false` the new version is only stored, not made live: visitors keep seeing the current one, and the answer carries a preview link (anyone with it can open it, for " + span(limits.PreviewLinkTTL) + ") to look at it first; make it live with rollback_site when the person is happy. `author` names who made the version in list_versions.",
			InputSchema: object(map[string]any{
				"site":             str(siteDesc),
				"files":            filesSchema(),
				"files_base64":     filesBase64Schema(),
				"keep_other_files": map[string]any{"type": "boolean", "description": "Keep unchanged files from the live version; default false."},
				"remove_files":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Paths to remove; only with keep_other_files."},
				"edits": map[string]any{"type": "array", "minItems": 1, "maxItems": maxEdits, "description": "Small changes to live text files, instead of sending them whole; every other file is kept.",
					"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"path", "find", "replace"}, "properties": map[string]any{
						"path":    str("File to change, e.g. `index.html`."),
						"find":    str("Exact text now in the file; it must occur exactly once."),
						"replace": str("Text to put in its place."),
					}}},
				"publish": map[string]any{"type": "boolean", "description": "Default true: the new version goes live at once. false stores it without making it live, and returns a preview link."},
				"author":  map[string]any{"type": "string", "maxLength": maxVersionAuthor, "description": "Who made this version, shown in list_versions; free text, for example a person's name."},
			}, "site"),
			// Overwrites what is live (destructive, even though rollback_site
			// can restore it) and publishes to the public web.
			Annotations: writes(true, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "replace") },
		},
		{
			policy:       toolPolicy{hackSiteExcluded: true},
			OutputSchema: outObject(map[string]any{"name": outString("Site name."), "keep_versions": outInteger("Stored setting."), "effective_keep_versions": outInteger("Applied count."), "active_version": outInteger("Live version."), "removed_versions": outArray("Removed versions.", outInteger("Version number."))}, "name", "keep_versions", "effective_keep_versions", "active_version", "removed_versions"),
			Name:         "set_keep_versions", Title: "Set versions kept",
			Description: "Set a site's version count; older versions are removed for good. Only accounts enabled by the operator may change it. Other accounts get: Simple Host keeps your 4 latest versions. 0 uses the account default. The live version always stays.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "keep_versions": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000, "description": "How many versions to keep (the live one included); 0 goes back to the account default."}}, "site", "keep_versions"),
			Annotations: writes(true, false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]any{"keep_versions": args["keep_versions"]})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/keep-versions", body, nil)
				if !res.ok() {
					return output{}, restError("set_keep_versions", res)
				}
				var out map[string]any
				_ = json.Unmarshal(res.body, &out)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			OutputSchema: outObject(map[string]any{
				"site": outString(outSiteName),
				"versions": outArray("Kept versions, newest first.", outObject(map[string]any{
					"version":      outInteger("Version number, for rollback_site."),
					"live":         outBool("Whether visitors see this version now."),
					"published_at": outString("When the version was published (RFC 3339)."),
					"not_yet_live": outBool("Present (true) for a version stored with update_site publish: false that has never been live; preview_version shows it, rollback_site makes it live."),
				}, "version", "live", "published_at")),
			}, "site", "versions"),
			Name:        "list_versions",
			Title:       "List a site's versions",
			Description: "List the versions kept for a site, newest first, with which one is live. Call this before rollback_site. Each version in the text may name its author (whoever the deployer stated); a version marked not_yet_live can be made live with rollback_site, or removed with discard_version where that tool is offered. `pending: true` lists only those not-yet-live versions.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"pending": map[string]any{"type": "boolean", "description": "true lists only versions stored with publish: false that have never been live."},
			}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				pending := false
				if raw, present := args["pending"]; present && raw != nil {
					b, ok := raw.(bool)
					if !ok {
						return output{}, errors.New("pending must be true or false")
					}
					pending = b
				}
				path := "/v1/sites/" + url.PathEscape(name) + "/versions"
				if pending {
					path += "?pending=1"
				}
				res := c.do(http.MethodGet, path, nil, nil)
				if !res.ok() {
					return output{}, restError("list_versions", res)
				}
				var raw []struct {
					VersionNumber int    `json:"version_number"`
					CreatedAt     string `json:"created_at"`
					IsActive      bool   `json:"is_active"`
					Status        string `json:"status"`
					Author        string `json:"author"`
				}
				_ = json.Unmarshal(res.body, &raw)
				sort.SliceStable(raw, func(i, j int) bool { return raw[i].VersionNumber > raw[j].VersionNumber })
				// When each version was published is what a person picks a
				// rollback by ("the one from yesterday"), so it stays; nothing
				// else about the stored version is shown. The author goes in
				// the text only: the structured output's schema is cached by
				// clients and refuses keys it does not know.
				versions := make([]any, 0, len(raw))
				told := make([]any, 0, len(raw))
				for _, v := range raw {
					item := map[string]any{"version": v.VersionNumber, "live": v.IsActive, "published_at": v.CreatedAt}
					if v.Status == "ready" && !v.IsActive {
						item["not_yet_live"] = true
					}
					versions = append(versions, item)
					said := make(map[string]any, len(item)+1)
					for k, val := range item {
						said[k] = val
					}
					if v.Author != "" {
						said["author"] = v.Author
					}
					told = append(told, said)
				}
				out := map[string]any{"site": name, "versions": versions}
				said := map[string]any{"site": name, "versions": told}
				if pending {
					said["pending_only"] = true
				}
				return output{Text: jsonText(said), Structured: out}, nil
			},
		},
		{
			OutputSchema: siteSummarySchema(),
			Name:         "rollback_site",
			Title:        "Make an earlier version live",
			Description:  "Make one of a site's kept versions live (visitors see it immediately): an earlier one, or one stored with update_site `publish: false` after the person has looked at its preview. Nothing is deleted; the current version stays available and can be restored the same way. Confirm the version with the person first.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The version number to make live, from list_versions."},
			}, "site", "version"),
			// Changes what the public sees; nothing is deleted and the
			// version it replaces can be made live again the same way.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]int{"version_number": version})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/active-version", body, nil)
				if !res.ok() {
					return output{}, restError("rollback_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				out := site.summary()
				return output{Text: fmt.Sprintf("Version %d of %s is now live at %s", site.ActiveVersion, name, site.liveURL()), Structured: out}, nil
			},
		},
		{
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"version":    outInteger("The version the link shows."),
				"live":       outBool("Whether this version is the one visitors see now."),
				"url":        outString("The preview link: works for anyone who has it, for " + span(limits.PreviewLinkTTL) + ", that version only. Give it to the person to open; do not post it publicly."),
				"expires_at": outString("When the link stops working (RFC 3339)."),
			}, "site", "version", "live", "url", "expires_at"),
			policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			Name:   "preview_version",
			Title:  "Preview a version before it is live",
			Description: "Make a preview link for one of a site's kept versions (from list_versions): the person opens it in their browser to see that version exactly as visitors would, before making it live with rollback_site. " +
				"The link works for anyone who has it, for " + span(limits.PreviewLinkTTL) + " and only for that version; pages opened from it cannot save anything, and search engines do not index it. Give it to the person to click; do not post it anywhere public.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The version number to preview, from list_versions."},
			}, "site", "version"),
			// Computes a signed preview URL without persisting a grant.
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version)+"/preview-link", nil, nil)
				if !res.ok() {
					return output{}, restError("preview_version", res)
				}
				var link struct {
					URL       string `json:"url"`
					ExpiresAt string `json:"expires_at"`
					Live      bool   `json:"live"`
				}
				_ = json.Unmarshal(res.body, &link)
				if link.URL == "" {
					return output{}, errors.New("preview_version failed: the server returned no link; try again in a moment")
				}
				out := map[string]any{"site": name, "version": version, "live": link.Live, "url": link.URL, "expires_at": link.ExpiresAt}
				text := fmt.Sprintf("Preview of %s version %d (valid until %s): %s", name, version, link.ExpiresAt, link.URL)
				if !link.Live {
					text += fmt.Sprintf("\nIt is not live; rollback_site with version %d makes it live.", version)
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			policy: toolPolicy{hackSiteExcluded: true},
			OutputSchema: outObject(map[string]any{
				"site":              outString(outSiteName),
				"discarded_version": outInteger("The never-live version that was removed for good."),
				"active_version":    outInteger("The version visitors see, unchanged."),
			}, "site", "discarded_version", "active_version"),
			Name:  "discard_version",
			Title: "Discard a version that was never live",
			Description: "DESTRUCTIVE: removes, for good, a version stored with update_site `publish: false` that has never been live. The live version and versions that have been live cannot be discarded (retention prunes those). " +
				"Confirm with the person first, naming the version number from list_versions.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The never-live version to discard, from list_versions (marked not_yet_live)."},
			}, "site", "version"),
			// Removes stored files for good; nothing the public sees changes.
			Annotations: writes(true, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodDelete, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version), nil, nil)
				if !res.ok() {
					return output{}, restError("discard_version", res)
				}
				var body struct {
					Site      string `json:"site"`
					Discarded int    `json:"discarded_version"`
					Active    int    `json:"active_version"`
				}
				_ = json.Unmarshal(res.body, &body)
				if body.Site == "" {
					body.Site = name
				}
				if body.Discarded == 0 {
					body.Discarded = version
				}
				out := map[string]any{"site": body.Site, "discarded_version": body.Discarded, "active_version": body.Active}
				return output{Text: fmt.Sprintf("Discarded version %d of %s; version %d stays live.", body.Discarded, body.Site, body.Active), Structured: out}, nil
			},
		},
		{
			policy: toolPolicy{hackSite: hackSiteDeleteSite},
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
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				confirm, err := stringArg(args, "confirm_name")
				if err != nil {
					return output{}, err
				}
				if strings.ToLower(confirm) != name {
					return output{}, fmt.Errorf("confirm_name %q does not match site %q; nothing was deleted", confirm, name)
				}
				res := c.do(http.MethodDelete, "/v1/sites/"+url.PathEscape(name), nil, nil)
				if !res.ok() {
					return output{}, restError("delete_site", res)
				}
				return output{Text: "Deleted " + name + ". It is offline now and stays in Recently deleted for " + span(limits.DeletedRetention) + "; restore_site brings it back with all its versions and data.", Structured: map[string]any{"deleted": name, "restorable_days": int(limits.DeletedRetention / (24 * time.Hour))}}, nil
			},
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
			run: func(c *call, _ map[string]any) (output, error) {
				res := c.do(http.MethodGet, "/v1/me/deleted-sites", nil, nil)
				if !res.ok() {
					return output{}, restError("list_deleted_sites", res)
				}
				var body struct {
					Sites []struct {
						Name      string `json:"name"`
						DeletedAt string `json:"deleted_at"`
						PurgeAt   string `json:"purge_at"`
					} `json:"sites"`
				}
				_ = json.Unmarshal(res.body, &body)
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
			},
		},
		{
			policy:       toolPolicy{hackSite: hackSiteRestoreSite},
			OutputSchema: withDescription(siteSummarySchema(), "The restored site, live again at its address."),
			Name:         "restore_site",
			Title:        "Restore a deleted site",
			Description:  "Bring back a site from Recently deleted (see list_deleted_sites): same name, same address, every version, saved data and connected address, live again at once.",
			InputSchema:  object(map[string]any{"site": str(siteDesc)}, "site"),
			// Serves the site again at its public address; nothing is lost.
			Annotations: writes(false, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/restore", nil, nil)
				if !res.ok() {
					return output{}, restError("restore_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				return output{Text: "Restored " + site.Name + ". Live again at " + site.liveURL(), Structured: site.summary()}, nil
			},
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
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				newName, err := stringArg(args, "new_name")
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"name": strings.ToLower(newName)})
				res := c.do(http.MethodPatch, "/v1/sites/"+url.PathEscape(name), body, nil)
				if !res.ok() {
					return output{}, restError("rename_site", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				return output{Text: "Renamed to " + site.Name + ". New address: " + site.liveURL(), Structured: site.summary()}, nil
			},
		},
		{
			policy:       toolPolicy{hackSiteExcluded: true},
			OutputSchema: outObject(map[string]any{"site": map[string]any{"type": []string{"string", "null"}}}, "site"),
			Name:         "set_home_page",
			Title:        "Choose my home page",
			Description:  "Make one of the person's sites their home page, served at their personal address https://<handle>.simple-host.app/ instead of the showcase (the list of their sites); site null brings the showcase back. The site's own address keeps working too. If that site goes offline or is deleted, the showcase shows again.",
			InputSchema:  object(map[string]any{"site": map[string]any{"type": []string{"string", "null"}, "description": "Your site's name, or null for the showcase."}}, "site"),
			Annotations:  writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				site, ok := args["site"]
				if !ok {
					return output{}, fmt.Errorf("site is required (name or null)")
				}
				if site != nil {
					if _, ok := site.(string); !ok {
						return output{}, fmt.Errorf("site must be a name or null")
					}
				}
				body, _ := json.Marshal(map[string]any{"site": site})
				res := c.do(http.MethodPut, "/v1/me/home", body, nil)
				if !res.ok() {
					return output{}, restError("set_home_page", res)
				}
				var result map[string]any
				_ = json.Unmarshal(res.body, &result)
				return output{Text: "Home page updated.", Structured: result}, nil
			},
		},
		{
			policy: toolPolicy{hackSite: hackSiteSetVisibility},
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"visibility": outEnum("public: listed on the account's public page; unlisted: left off it, and out of search engines unless the site has its own domain or name (still public to anyone with the address).", "public", "unlisted"),
			}, "site", "visibility"),
			Name:  "set_visibility",
			Title: "Show or hide a site on my public page",
			Description: "Choose whether a site is listed on the account's public page (https://<handle>.simple-host.app/). An unlisted site is also kept out of search engines unless it has its own domain or claimed name (noindex on every address; its own robots.txt, if it ships one, is kept). " +
				"This is NOT privacy: an unlisted site is still public to anyone with its address; never describe unlisted as private. To ask visitors for a passcode on the whole site, use set_site_passcode.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"visibility": map[string]any{"type": "string", "enum": []string{"public", "unlisted"}, "description": "`public` lists it on the public page; `unlisted` leaves it off, and out of search engines unless the site has its own domain or name (still reachable by its address)."},
			}, "site", "visibility"),
			// Adds a site to, or removes it from, a public listing page.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				vis, err := stringArg(args, "visibility")
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"visibility": vis})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/visibility", body, nil)
				if !res.ok() {
					return output{}, restError("set_visibility", res)
				}
				return output{Text: name + " is now " + strings.ToLower(vis) + ".", Structured: map[string]any{"site": name, "visibility": strings.ToLower(vis)}}, nil
			},
		},
		{
			policy: toolPolicy{hackSite: hackSiteSetSiteOffline},
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
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				off, ok := args["offline"].(bool)
				if !ok {
					return output{}, errors.New("offline is required: true to take the site offline, false to put it back online")
				}
				body, _ := json.Marshal(map[string]bool{"offline": off})
				res := c.do(http.MethodPatch, "/v1/sites/"+url.PathEscape(name), body, nil)
				if !res.ok() {
					return output{}, restError("set_site_offline", res)
				}
				var site restSite
				_ = json.Unmarshal(res.body, &site)
				out := map[string]any{"site": name, "offline": site.Offline, "url": site.liveURL()}
				text := name + " is back online at " + site.liveURL()
				if site.Offline {
					text = name + " is offline: " + site.liveURL() + " shows \"This site is offline\" and visitor saves are refused. Nothing was deleted."
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			OutputSchema: outObject(map[string]any{
				"site": outString(outSiteName),
				"keep": outBool("true: kept up for good, never flagged as idle; false: flagged (with an email first) after " + span(limits.IdleAfter) + " without visits or updates."),
			}, "site", "keep"),
			Name:  "keep_site",
			Title: "Keep a site up for good",
			Description: "Mark a site Keep (or clear the mark). Simple Host emails the owner about a site nobody has visited or updated for " + span(limits.IdleAfter) + " and moves it to Recently deleted " + span(limits.IdleGrace) + " later unless it is kept; a site marked Keep is never flagged. " +
				"Sites with their own domain or claimed name are never flagged either.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"keep": map[string]any{"type": "boolean", "description": "true (default) keeps the site up for good; false lets it be flagged again when idle."},
			}, "site"),
			// Changes a flag on the person's own site; reversible.
			Annotations: writes(true, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				keep := true
				if v, ok := args["keep"]; ok {
					b, isBool := v.(bool)
					if !isBool {
						return output{}, errors.New("keep must be true or false")
					}
					keep = b
				}
				body, _ := json.Marshal(map[string]bool{"keep": keep})
				res := c.do(http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/keep", body, nil)
				if !res.ok() {
					return output{}, restError("keep_site", res)
				}
				text := name + " is marked Keep: it stays up even if nobody visits it."
				if !keep {
					text = name + " is no longer marked Keep: if nobody visits or updates it for " + span(limits.IdleAfter) + ", its owner is emailed before anything happens."
				}
				return output{Text: text, Structured: map[string]any{"site": name, "keep": keep}}, nil
			},
		},
		{
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"range_days": outInteger("The window the totals cover, in days."),
				"totals":     trafficSplitSchema("Traffic over the whole window. Visitors are unique across the window."),
				"last_24h":   trafficSplitSchema("Traffic over the last 24 hours."),
				"top_pages": outArray("The most viewed pages over the window, people only, most first.", outObject(map[string]any{
					"path":  outString("The page's path on the site."),
					"views": outInteger("Views by people."),
				}, "path", "views")),
				"top_referrers": outArray("Where visitors came from over the window: the referring domain only (never a full address), people only, most first.", outObject(map[string]any{
					"domain": outString("The referring site's domain."),
					"views":  outInteger("Views by people arriving from it."),
				}, "domain", "views")),
			}, "site", "range_days", "totals", "last_24h"),
			Name:        "site_analytics",
			Title:       "How many people visited",
			Description: "Visits to a site over the last N days, split into people (not counting the owner's own visits), bots and monitoring, with the most viewed pages and the domains visitors came from (people only). When asked how many people visited, report the `person` numbers only.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"days": map[string]any{"type": "integer", "description": "Window in days (1–90, default 30)."},
			}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				days, given, err := wholeNumber(args, "days", false, 1, 90)
				if err != nil {
					return output{}, err
				}
				if !given {
					days = 30
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics?days="+strconv.Itoa(days), nil, nil)
				if !res.ok() {
					return output{}, restError("site_analytics", res)
				}
				var full map[string]any
				_ = json.Unmarshal(res.body, &full)
				// Totals only: the daily and hourly series are for charts and
				// would swamp a conversation.
				// The structured split stays person/bot/infra/unknown: the
				// owner's own visits (class owner) are stated in the text.
				out := map[string]any{"site": name, "range_days": full["range_days"], "totals": withoutOwner(full["totals"]), "last_24h": withoutOwner(full["last_24h"])}
				// Top pages and referring domains are a second read; the
				// totals stand without them if it fails.
				if top := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics/top?days="+strconv.Itoa(days), nil, nil); top.ok() {
					var t struct {
						Pages     []any `json:"pages"`
						Referrers []any `json:"referrers"`
					}
					if json.Unmarshal(top.body, &t) == nil && t.Pages != nil && t.Referrers != nil {
						out["top_pages"], out["top_referrers"] = t.Pages, t.Referrers
					}
				}
				text := jsonText(out)
				if c.caller.Mode != CallerModeHackSite {
					text += "\n\n" + ownVisitsLine(full["totals"])
				}
				answer := output{Text: text, Structured: out}
				if c.server.dashboardEnabled(c.caller) {
					answer.Meta = map[string]any{"simple-host/daily": full["daily"]}
				}
				return answer, nil
			},
		},
		{
			policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}, hackSite: hackSiteExportSite},
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"url":        outString("The download link: a .tar.gz with the site's files, state.json and collections.json. Give it to the person; it opens without signing in, so do not post it publicly."),
				"expires_at": outString("When the link stops working (RFC 3339), " + span(limits.ExportLinkTTL) + " after it was made."),
			}, "site", "url", "expires_at"),
			Name:  "export_site",
			Title: "Download a copy of a site",
			Description: "Make a download link for a copy of one of the person's sites: a .tar.gz holding its live files and its saved data " +
				"(storage resources with their KV values, SQLite databases, and files, plus any older state and collections). The link works for " + span(limits.ExportLinkTTL) + " and only for that site; " +
				"give it to the person to click, and make a new one if it has expired. Do not post it anywhere public: until it expires, anyone with it can download the copy.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			// Computes a signed download URL without creating an export job.
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/export-link", nil, nil)
				if !res.ok() {
					return output{}, restError("export_site", res)
				}
				var link struct {
					Site      string `json:"site"`
					URL       string `json:"url"`
					ExpiresAt string `json:"expires_at"`
				}
				_ = json.Unmarshal(res.body, &link)
				if link.URL == "" {
					return output{}, errors.New("export_site failed: the server returned no link; try again in a moment")
				}
				if link.Site == "" {
					link.Site = name
				}
				out := map[string]any{"site": link.Site, "url": link.URL, "expires_at": link.ExpiresAt}
				text := fmt.Sprintf("Download link for a copy of %s (files, saved state and collections), valid until %s: %s", link.Site, link.ExpiresAt, link.URL)
				return output{Text: text, Structured: out}, nil
			},
		},
	}
}

// siteNotFoundText names the account's sites (up to 20) next to the name
// that matched none of them.
func siteNotFoundText(name string, names []string) string {
	text := fmt.Sprintf("no site named %q in this account", name)
	// Names that share or contain the asked-for name come first.
	close := func(n string) bool {
		return strings.Contains(n, name) || strings.Contains(name, n) || len(name) > 3 && len(n) > 3 && n[:4] == name[:4]
	}
	sorted := make([]string, 0, len(names))
	for _, n := range names {
		if close(n) {
			sorted = append(sorted, n)
		}
	}
	for _, n := range names {
		if !close(n) {
			sorted = append(sorted, n)
		}
	}
	names = sorted
	switch {
	case len(names) == 0:
		return text + ", which has no sites yet. create_site makes one"
	case len(names) > 20:
		text += ". Sites here include: " + strings.Join(names[:20], ", ") + fmt.Sprintf(" and %d more (list_sites shows all)", len(names)-20)
	default:
		text += ". Sites here: " + strings.Join(names, ", ")
	}
	return text + ". A site owned by someone else cannot be changed from here"
}

// ---- site helpers -----------------------------------------------------------

// restSite is the subset of the REST site object the tools report.
type restSite struct {
	UpdatedAt string `json:"updated_at"`
	Summary   *struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Headings    string `json:"headings"`
		Text        string `json:"text"`
	} `json:"summary"`
	FileBytes         int64  `json:"file_bytes"`
	Name              string `json:"name"`
	ActiveVersion     int    `json:"active_version"`
	SiteURL           string `json:"site_url"`
	CustomDomain      string `json:"custom_domain"`
	DomainStatus      string `json:"domain_status"`
	Visibility        string `json:"visibility"`
	Offline           bool   `json:"offline"`
	Access            string `json:"access"`
	PasscodeProtected bool   `json:"passcode_protected"`
	Suspended         bool   `json:"suspended"`
	// FamilyAddress is the site's address under its account's address
	// family, when one serves it (the address handed out after a domain).
	FamilyAddress string `json:"family_address"`
	// AddressState is present while the site is at its interim address.
	AddressState *struct {
		Note string `json:"note"`
	} `json:"address_state"`
	// Views is the site list's summary of views by people over rolling
	// windows; the owner's own visits are left out. Said in text only: the
	// cached output schemas in chat apps reject new structured keys.
	Views *struct {
		People7Days  int64 `json:"people_7_days"`
		People30Days int64 `json:"people_30_days"`
	} `json:"views"`
}

// liveURL is the one address to give people: a connected, working custom
// domain wins, then the site's address under its account's address family,
// because the site's own address redirects there.
func (s restSite) liveURL() string {
	if s.CustomDomain != "" && s.DomainStatus == "active" {
		return "https://" + s.CustomDomain + "/"
	}
	if s.FamilyAddress != "" {
		return s.FamilyAddress
	}
	return s.SiteURL
}

func (s restSite) summary() map[string]any {
	m := map[string]any{
		"name":           s.Name,
		"file_bytes":     s.FileBytes,
		"url":            s.liveURL(),
		"active_version": s.ActiveVersion,
		"listed":         s.Visibility == "public",
	}
	if s.CustomDomain != "" {
		m["custom_domain"] = s.CustomDomain
		m["domain_status"] = s.DomainStatus
	}
	if s.AddressState != nil && s.AddressState.Note != "" {
		m["address_note"] = s.AddressState.Note
	}
	if s.Summary != nil {
		fields := map[string]any{}
		for key, value := range map[string]string{"title": s.Summary.Title, "description": s.Summary.Description, "headings": s.Summary.Headings, "text": s.Summary.Text} {
			if value != "" {
				fields[key] = value
			}
		}
		if len(fields) > 0 {
			m["summary"] = fields
		}
	}

	return m
}

func (c *call) listSites() ([]restSite, error) {
	res := c.do(http.MethodGet, "/v1/sites", nil, nil)
	if !res.ok() {
		return nil, restError("list_sites", res)
	}
	var sites []restSite
	if err := json.Unmarshal(res.body, &sites); err != nil {
		return nil, errors.New("list_sites: unexpected answer from the server")
	}
	return sites, nil
}

func (c *call) findSite(name string) (restSite, error) {
	sites, err := c.listSites()
	if err != nil {
		return restSite{}, err
	}
	names := make([]string, 0, len(sites))
	for _, s := range sites {
		if s.Name == name {
			return s, nil
		}
		names = append(names, s.Name)
	}
	text := siteNotFoundText(name, names)
	if c.caller.Mode != CallerModeEvents && c.caller.Mode != CallerModeHackSite {
		// Simple Hack lists no visitor tools.
		text += "; for a site shared with you, call visitor_read_site with its address (through run_tool if it is not listed)"
	}
	return restSite{}, codedError("site_not_found", text)
}

// ownerViewsLine states each site's views by people (the owner's own visits
// left out) for list_sites' text; "" when the server sent no views at all.
func ownerViewsLine(sites []restSite) string {
	parts := make([]string, 0, len(sites))
	found := false
	for _, s := range sites {
		if s.Views == nil {
			parts = append(parts, s.Name+" 0 / 0")
			continue
		}
		found = true
		parts = append(parts, s.Name+" "+strconv.FormatInt(s.Views.People7Days, 10)+" / "+strconv.FormatInt(s.Views.People30Days, 10))
	}
	if !found {
		return ""
	}
	return "Views by people, not counting your own visits (last 7 days / last 30 days): " + strings.Join(parts, "; ") + "." + ownVisitsCaveat
}

// withoutOwner copies an analytics split and drops its owner class, so the
// structured result keeps exactly person, bot, infra and unknown.
func withoutOwner(split any) any {
	m, ok := split.(map[string]any)
	if !ok {
		return split
	}
	copied := make(map[string]any, len(m))
	for k, v := range m {
		if k != "owner" {
			copied[k] = v
		}
	}
	return copied
}

// ownVisitsLine reads the owner class out of the totals split and says it in
// words, so the person hears their own visits were left out of person.
func ownVisitsLine(totals any) string {
	views, visitors := 0.0, 0.0
	if m, ok := totals.(map[string]any); ok {
		if o, ok := m["owner"].(map[string]any); ok {
			views, _ = o["views"].(float64)
			visitors, _ = o["visitors"].(float64)
		}
	}
	if views == 0 {
		return "Your own visits, left out of person: 0 views in this window." + ownVisitsCaveat
	}
	return "Your own visits, left out of person: " + strconv.FormatFloat(views, 'f', -1, 64) + " views from " +
		strconv.FormatFloat(visitors, 'f', -1, 64) + " of your addresses in this window." + ownVisitsCaveat
}

// ownVisitsCaveat goes wherever the owner-excluded figures do (INTENT.md,
// 2026-11-02 "Your own visits are not counted as people").
const ownVisitsCaveat = " A device never used while signed in to Simple Host still counts as a person, and other people behind an address you were signed in from are left out while that mark lasts (30 days)."

// maxFileText bounds how much of one file read_site_file hands back, so one
// call cannot flood the conversation.
const maxFileText = 200 << 10

func filesSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Map of file path → text content, e.g. {\"index.html\": \"<!DOCTYPE html>…\", \"css/style.css\": \"body{…}\"}. Paths are relative, no leading slash, no `..`.",
		"additionalProperties": map[string]any{"type": "string"},
	}
}

func filesBase64Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps.",
		"additionalProperties": map[string]any{"type": "string"},
	}
}

const maxReadPaths = 10

// siteFile is one file read from a site version.
type siteFile struct {
	path      string
	size      int
	content   string
	binary    bool
	truncated bool
	ctype     string
}

func (f siteFile) structured() map[string]any {
	out := map[string]any{"path": f.path, "size": f.size}
	if f.binary {
		out["binary"] = true
	} else {
		out["content"] = f.content
	}
	if f.truncated {
		out["truncated"] = true
	}
	return out
}

// readFile reads one file of a site version through the REST API.
func readFile(c *call, tool, site string, version int, path string) (siteFile, error) {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(site)+"/versions/"+strconv.Itoa(version)+"/files/"+strings.Join(segments, "/"), nil, nil)
	if !res.ok() {
		return siteFile{}, restError(tool, res)
	}
	f := siteFile{path: path, size: len(res.body), ctype: res.header.Get("Content-Type")}
	textual := utf8.Valid(res.body) && !bytes.ContainsRune(res.body, 0) &&
		!strings.HasPrefix(f.ctype, "image/") && !strings.HasPrefix(f.ctype, "font/") && !strings.HasPrefix(f.ctype, "audio/") && !strings.HasPrefix(f.ctype, "video/")
	if !textual {
		f.binary = true
		return f, nil
	}
	f.content = string(res.body)
	if len(f.content) > maxFileText {
		f.content = f.content[:maxFileText]
		for !utf8.ValidString(f.content) {
			f.content = f.content[:len(f.content)-1]
		}
		f.truncated = true
	}
	return f, nil
}

func readSiteFiles(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	var paths []string
	_, single := args["path"]
	if raw, present := args["paths"]; present {
		if single {
			return output{}, errors.New("give path or paths, not both")
		}
		list, ok := listArg(raw)
		if !ok || len(list) == 0 || len(list) > maxReadPaths {
			return output{}, fmt.Errorf("paths must be a list of 1 to %d file paths", maxReadPaths)
		}
		for _, item := range list {
			p, ok := item.(string)
			if !ok || p == "" {
				return output{}, errors.New("paths must hold file paths as strings")
			}
			paths = append(paths, p)
		}
	} else {
		path, err := stringArg(args, "path")
		if err != nil {
			return output{}, errors.New("path is required (or paths, for several files)")
		}
		paths = []string{path}
	}
	version, given, err := wholeNumber(args, "version", false, 1, math.MaxInt32)
	if err != nil {
		return output{}, err
	}
	if !given {
		site, err := c.findSite(name)
		if err != nil {
			return output{}, err
		}
		version = site.ActiveVersion
	}
	if !single {
		files := make([]any, 0, len(paths))
		var text strings.Builder
		for _, path := range paths {
			f, err := readFile(c, "read_site_file", name, version, path)
			if err != nil {
				// One missing file does not hide the others.
				files = append(files, map[string]any{"path": path, "error": err.Error()})
				fmt.Fprintf(&text, "=== %s ===\n%s\n", path, err.Error())
				continue
			}
			files = append(files, f.structured())
			if f.binary {
				fmt.Fprintf(&text, "=== %s ===\n(binary file, %s, %d bytes; contents not shown)\n", path, f.ctype, f.size)
			} else {
				fmt.Fprintf(&text, "=== %s ===\n%s\n", path, f.content)
			}
		}
		out := map[string]any{"site": name, "version": version, "files": files}
		return output{Text: text.String(), Structured: out}, nil
	}
	f, err := readFile(c, "read_site_file", name, version, paths[0])
	if err != nil {
		return output{}, err
	}
	out := f.structured()
	out["site"], out["version"] = name, version
	if f.binary {
		return output{Text: fmt.Sprintf("%s is a binary file (%s, %d bytes); its contents are not shown.", f.path, f.ctype, f.size), Structured: out}, nil
	}
	return output{Text: f.content, Structured: out}, nil
}

const maxEdits = 50

// applyEdits reads each edited file from the live version, replaces each
// find text (which must occur exactly once), and returns the new contents.
func applyEdits(c *call, site string, raw any) (map[string]string, error) {
	list, ok := listArg(raw)
	if !ok || len(list) == 0 || len(list) > maxEdits {
		return nil, fmt.Errorf("edits must be a list of 1 to %d {path, find, replace} changes", maxEdits)
	}
	live, err := c.findSite(site)
	if err != nil {
		return nil, err
	}
	if live.ActiveVersion < 1 {
		return nil, fmt.Errorf("%s has no live version to edit; send whole files instead", site)
	}
	texts := map[string]string{}
	for i, item := range list {
		edit, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d] must be an object with path, find, and replace", i)
		}
		for key := range edit {
			if key != "path" && key != "find" && key != "replace" {
				return nil, fmt.Errorf("edits[%d] has unexpected field %q; it takes path, find, and replace", i, key)
			}
		}
		path, _ := edit["path"].(string)
		find, _ := edit["find"].(string)
		replace, okReplace := edit["replace"].(string)
		if path == "" || find == "" || !okReplace {
			return nil, fmt.Errorf("edits[%d] needs path, a non-empty find, and replace (all strings)", i)
		}
		text, seen := texts[path]
		if !seen {
			f, err := readFile(c, "update_site", site, live.ActiveVersion, path)
			if err != nil {
				return nil, fmt.Errorf("edits[%d]: could not read %s: %w", i, path, err)
			}
			if f.binary || f.truncated {
				return nil, fmt.Errorf("edits[%d]: %s is not a text file under 200 KB; send it whole in files", i, path)
			}
			text = f.content
		}
		switch n := strings.Count(text, find); n {
		case 1:
			texts[path] = strings.Replace(text, find, replace, 1)
		case 0:
			return nil, fmt.Errorf("edits[%d]: the find text is not in %s, so nothing was published. Copy it exactly from read_site_file (spaces and line breaks count)", i, path)
		default:
			return nil, fmt.Errorf("edits[%d]: the find text occurs %d times in %s, so nothing was published. Include more of the surrounding text so it occurs once", i, n, path)
		}
	}
	return texts, nil
}

// maxVersionAuthor is the longest author a version keeps (the REST route
// trims and caps at the same length).
const maxVersionAuthor = 80

// deploySite publishes the given files. mode is "create" (a new site only,
// never overwriting) or "replace" (an existing site only).
func deploySite(c *call, args map[string]any, mode string) (output, error) {
	tool := "create_site"
	if mode == "replace" {
		tool = "update_site"
	}
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	files, err := stringMap(args, "files")
	if err != nil {
		return output{}, err
	}
	keep := false
	if value, present := args["keep_other_files"]; present {
		var ok bool
		keep, ok = value.(bool)
		if !ok {
			return output{}, errors.New("keep_other_files must be true or false")
		}
	}
	if keep && mode != "replace" {
		return output{}, errors.New("keep_other_files is only for update_site")
	}
	var edited []string
	if raw, present := args["edits"]; present {
		if mode != "replace" {
			return output{}, errors.New("edits is only for update_site")
		}
		texts, err := applyEdits(c, name, raw)
		if err != nil {
			return output{}, err
		}
		if files == nil {
			files = map[string]string{}
		}
		for path, text := range texts {
			if _, dup := files[path]; dup {
				return output{}, fmt.Errorf("%s is in both files and edits; use one of them for a file", path)
			}
			files[path] = text
			edited = append(edited, path)
		}
		sort.Strings(edited)
		// An edit changes part of the live site, so the rest is always kept.
		keep = true
	}
	if _, exists := args["remove_files"]; exists && !keep {
		return output{}, errors.New("remove_files requires keep_other_files")
	}
	if len(files) == 0 && !keep {
		return output{}, errors.New("files is required: an object mapping each path to its contents, including index.html")
	}
	binary, err := stringMap(args, "files_base64")
	if err != nil {
		return output{}, err
	}
	for _, path := range edited {
		if _, dup := binary[path]; dup {
			return output{}, fmt.Errorf("%s is in both files_base64 and edits; use one of them for a file", path)
		}
	}
	if _, ok := files["index.html"]; !ok && !keep {
		if _, ok := binary["index.html"]; !ok {
			return output{}, errors.New("files must include index.html at the site root")
		}
	}
	payload := map[string]any{"files": files}
	if remove, exists := args["remove_files"]; exists {
		payload["remove_files"] = remove
	}
	if len(binary) > 0 {
		payload["files_base64"] = binary
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(name) + "/files"
	publish := true
	if raw, present := args["publish"]; present && raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return output{}, errors.New("publish must be true or false")
		}
		publish = b
	}
	if !publish {
		if mode == "create" {
			return output{}, errors.New("create_site always makes a new site live; publish: false is for update_site")
		}
		path += "?publish=false"
	}

	if keep {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "keep=1"
	}
	if raw, present := args["author"]; present && raw != nil {
		if mode != "replace" {
			return output{}, errors.New("author is only for update_site")
		}
		author, err := stringArg(args, "author")
		if err != nil {
			return output{}, err
		}
		author = strings.TrimSpace(strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return -1
			}
			return c
		}, author))
		if utf8.RuneCountInString(author) > maxVersionAuthor {
			return output{}, fmt.Errorf("author must be at most %d characters", maxVersionAuthor)
		}
		if author != "" {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			path += sep + "author=" + url.QueryEscape(author)
		}
	}
	var res upstreamResult
	if mode == "create" {
		res = c.do(http.MethodPost, path, body, nil)
	} else {
		res = c.do(http.MethodPut, path, body, nil)
	}
	if !res.ok() {
		_, refusalCode := upstreamMessage(res)
		switch {
		case res.status == http.StatusNotFound && mode == "replace":
			return output{}, fmt.Errorf("update_site failed: there is no site named %q in this account. Use create_site for a new site, or list_sites for existing names", name)
		case res.status == http.StatusConflict && mode == "create" && strings.Contains(string(res.body), `"recently_deleted":true`):
			return output{}, codedError("recently_deleted", fmt.Sprintf("create_site failed: a site named %q was deleted recently and is in Recently deleted, which keeps its name. Ask the person whether to bring it back with restore_site, or pick another name [recently_deleted]", name))
		case res.status == http.StatusConflict && mode == "create" && refusalCode == "site_exists":
			return output{}, fmt.Errorf("create_site failed: this account already has a site named %q, and create_site never overwrites. To change it, use update_site (read its files first); for a separate site, pick another name", name)
		}
		return output{}, restError(tool, res)
	}
	var site restSite
	_ = json.Unmarshal(res.body, &site)
	out := site.summary()
	out["file_count"] = len(files) + len(binary)
	warns := pageWarnings(c, name, files)
	warnText := ""
	if len(warns) > 0 {
		out["page_warnings"] = warns
		warnText = "\nFix these before telling the person the page works:\n- " + strings.Join(warns, "\n- ")
	}
	if !publish {
		var stored struct {
			Version    int    `json:"unpublished_version"`
			PreviewURL string `json:"preview_url"`
		}
		_ = json.Unmarshal(res.body, &stored)
		out["unpublished_version"] = stored.Version
		text := fmt.Sprintf("Stored %s version %d without making it live; visitors still see version %d at %s.", name, stored.Version, site.ActiveVersion, site.liveURL())
		if stored.PreviewURL != "" {
			out["preview_url"] = stored.PreviewURL
			text += " Preview it (the link works for anyone who has it, for " + span(c.server.cfg.Limits.PreviewLinkTTL) + "): " + stored.PreviewURL
		}
		text += fmt.Sprintf("\nWhen the person is happy, rollback_site with version %d makes it live.", stored.Version)
		return output{Text: text + warnText, Structured: out}, nil
	}
	verb := "Published a new version of"
	if mode == "create" {
		verb = "Created"
	}
	text := fmt.Sprintf("%s %s (version %d). Live at %s", verb, name, site.ActiveVersion, site.liveURL())
	// Said in the text, not a new structured field: apps that cache the
	// output schema (ChatGPT keeps it from the day it connected) refuse fields
	// they do not know.
	if len(edited) > 0 {
		text += "\nEdited " + strings.Join(edited, ", ") + "; every other file was kept."
	}
	return output{Text: text + warnText, Structured: out}, nil
}

// deployFormTip sits on create_site and update_site because a small model
// writing a contact form reads these descriptions, not the instructions.
const deployFormTip = "A form visitors send (contact, quote, booking, sign-up, order) must save to storage: call get_page_recipe first; never use a mailto: link or an outside form service. "

// deployPhotoTip: Simple Hack has no photos link and swaps in hackPhotoTip.
const deployPhotoTip = "For the person's own photos, give them create_upload_link's photos_url (it shrinks them); never ask them to resize or attach photos."

const hackPhotoTip = "Compress photos before deploying: about 1600 px wide, WebP or JPEG at about 80% quality; phone photos are often 4–12 MB."

const deploySizeTips = "Check who_am_i for account file usage and list_sites for site sizes before deploying. Hosted Simple Host keeps 4 versions; new sites have a 200 MB whole-file allowance and accounts 1 GB (operator exceptions apply). The separate live copy and every kept version count; saved data and storage resources do not. " + deployPhotoTip + " Keep zip files, installers and videos elsewhere and link to them. Remove unused files; ask the person which old sites they no longer need."
