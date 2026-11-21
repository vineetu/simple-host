package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

func siteVersionTools(limits Limits) []Tool {
	return []Tool{
		{
			policy:       toolPolicy{},
			OutputSchema: outObject(map[string]any{"name": outString("Site name."), "keep_versions": outInteger("Stored setting."), "effective_keep_versions": outInteger("Applied count."), "active_version": outInteger("Live version."), "removed_versions": outArray("Removed versions.", outInteger("Version number."))}, "name", "keep_versions", "effective_keep_versions", "active_version", "removed_versions"),
			Name:         "set_keep_versions", Title: "Set versions kept",
			Description: "Set a site's version count; older versions are removed for good. Only accounts enabled by the operator may change it. Other accounts get: Simple Host keeps your 4 latest versions. 0 uses the account default. The live version always stays.",
			InputSchema: object(map[string]any{"site": str(siteDesc), "keep_versions": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000, "description": "How many versions to keep (the live one included); 0 goes back to the account default."}}, "site", "keep_versions"),
			Annotations: writes(true, false, false),
			run:         runSetKeepVersions,
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
			run:         runListVersions,
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
			run:         runRollbackSite,
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
			run:         runPreviewVersion,
		},
		{
			policy: toolPolicy{},
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
			run:         runDiscardVersion,
		},
	}
}

func runListVersions(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	pending, err := boolArg(args, "pending", false)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(name) + "/versions"
	if pending {
		path += "?pending=1"
	}
	var raw []struct {
		VersionNumber int    `json:"version_number"`
		CreatedAt     string `json:"created_at"`
		IsActive      bool   `json:"is_active"`
		Status        string `json:"status"`
		Author        string `json:"author"`
	}
	if err := c.doJSON("list_versions", http.MethodGet, path, nil, &raw); err != nil {
		return output{}, err
	}
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
		said := maps.Clone(item)
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
}

// siteVersionArgs reads the site and the version number a version tool needs.
func siteVersionArgs(args map[string]any) (string, int, error) {
	name, err := siteArg(args)
	if err != nil {
		return "", 0, err
	}
	version, _, err := wholeNumber(args, "version", true, 1, math.MaxInt32)
	return name, version, err
}

func runSetKeepVersions(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	body, _ := json.Marshal(map[string]any{"keep_versions": args["keep_versions"]})
	var out map[string]any
	if err := c.doJSON("set_keep_versions", http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/keep-versions", body, &out); err != nil {
		return output{}, err
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func runRollbackSite(c *call, args map[string]any) (output, error) {
	name, version, err := siteVersionArgs(args)
	if err != nil {
		return output{}, err
	}
	body, _ := json.Marshal(map[string]int{"version_number": version})
	var site restSite
	if err := c.doJSON("rollback_site", http.MethodPut, "/v1/sites/"+url.PathEscape(name)+"/active-version", body, &site); err != nil {
		return output{}, err
	}
	return output{Text: fmt.Sprintf("Version %d of %s is now live at %s", site.ActiveVersion, name, site.liveURL()), Structured: site.summary()}, nil
}

func runPreviewVersion(c *call, args map[string]any) (output, error) {
	name, version, err := siteVersionArgs(args)
	if err != nil {
		return output{}, err
	}
	var link struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
		Live      bool   `json:"live"`
	}
	if err := c.doJSON("preview_version", http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version)+"/preview-link", nil, &link); err != nil {
		return output{}, err
	}
	if link.URL == "" {
		return output{}, errors.New("preview_version failed: the server returned no link; try again in a moment")
	}
	out := map[string]any{"site": name, "version": version, "live": link.Live, "url": link.URL, "expires_at": link.ExpiresAt}
	text := fmt.Sprintf("Preview of %s version %d (valid until %s): %s", name, version, link.ExpiresAt, link.URL)
	if !link.Live {
		text += fmt.Sprintf("\nIt is not live; rollback_site with version %d makes it live.", version)
	}
	return output{Text: text, Structured: out}, nil
}

func runDiscardVersion(c *call, args map[string]any) (output, error) {
	name, version, err := siteVersionArgs(args)
	if err != nil {
		return output{}, err
	}
	var body struct {
		Site      string `json:"site"`
		Discarded int    `json:"discarded_version"`
		Active    int    `json:"active_version"`
	}
	if err := c.doJSON("discard_version", http.MethodDelete, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version), nil, &body); err != nil {
		return output{}, err
	}
	if body.Site == "" {
		body.Site = name
	}
	if body.Discarded == 0 {
		body.Discarded = version
	}
	out := map[string]any{"site": body.Site, "discarded_version": body.Discarded, "active_version": body.Active}
	return output{Text: fmt.Sprintf("Discarded version %d of %s; version %d stays live.", body.Discarded, body.Site, body.Active), Structured: out}, nil
}
