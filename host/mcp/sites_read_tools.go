package mcp

// Sites family, read side: list, get and read files.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func siteReadTools() []Tool {
	return []Tool{
		{
			OutputSchema: outObject(map[string]any{
				"sites": outArray("Sites returned by the requested filters and page.", siteSummaryWith(map[string]any{
					"offline":     outBool("Present (true) only when the owner has taken the site offline: every address shows \"This site is offline\" and visitor saves are refused."),
					"owner":       outString("Present when owner named a team: that team's handle."),
					"access_role": outEnum("Present when owner named another account: member (you act on a team's sites as one of its members) or editor (you see and edit only the sites you edit).", "owner", "member", "editor"),
				})),
				"count": outInteger("Number of sites returned."),
			}, "sites", "count"),
			policy:      toolPolicy{screen: "my-sites"},
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
			run:         runListSites,
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
			run:         runGetSite,
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
	}
}

func runListSites(c *call, args map[string]any) (output, error) {
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
	all, err := boolArg(args, "all_accounts", false)
	if err != nil {
		return output{}, err
	}
	if all {
		v.Set("all", "1")
	}
	sites, err := c.listSites(v)
	if err != nil {
		return output{}, err
	}
	items := make([]any, 0, len(sites))
	for _, s := range sites {
		item := s.summary()
		if s.Offline {
			item["offline"] = true
		}
		if c.owner != "" && s.AccessRole != "" {
			item["owner"], item["access_role"] = s.Owner, s.AccessRole
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
	if views := ownerViewsLine(sites); views != "" {
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
}

func runGetSite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	// One listing: the account's sites, with the live files of this one.
	site, err := c.findSiteIn(name, url.Values{"files": {name}})
	if err != nil {
		return output{}, err
	}
	out := site.summary()
	if site.Files != nil {
		files := make([]any, 0, len(*site.Files))
		for _, f := range *site.Files {
			files = append(files, map[string]any{"path": f.Path, "size": f.Size})
		}
		out["files"] = files
	}
	answer := sitePreviewOutput(c, site, out)
	if site.Views != nil {
		answer.Text += "\n\nViews by people, not counting your own visits: " + strconv.FormatInt(site.Views.People7Days, 10) +
			" in the last 7 days, " + strconv.FormatInt(site.Views.People30Days, 10) + " in the last 30 days." + ownVisitsCaveat
	}
	return answer, nil
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
	LiveURL           string `json:"live_url"`
	CustomDomain      string `json:"custom_domain"`
	DomainStatus      string `json:"domain_status"`
	Visibility        string `json:"visibility"`
	Offline           bool   `json:"offline"`
	Access            string `json:"access"`
	PasscodeProtected bool   `json:"passcode_protected"`
	Suspended         bool   `json:"suspended"`
	OpenToAnyone      bool   `json:"open_to_anyone"`
	Owner             string `json:"owner"`
	AccessRole        string `json:"access_role"`
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
	// Files is the live version's files, on a listing asked with
	// ?files=<this site>.
	Files *[]struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"files"`
}

// liveURL is the one address to give people: REST's live_url (sites.LiveURL
// decides it), or the site_url of a REST response that has no live_url (a
// rename). The tool never works an address out itself.
func (s restSite) liveURL() string {
	if s.LiveURL != "" {
		return s.LiveURL
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

// listSites reads the account's sites, filtered by query when it has any.
func (c *call) listSites(query url.Values) ([]restSite, error) {
	path := "/v1/sites"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	res := c.do(http.MethodGet, path, nil, nil)
	if !res.ok() {
		return nil, restError("list_sites", res)
	}
	var sites []restSite
	if err := json.Unmarshal(res.body, &sites); err != nil {
		return nil, errors.New("list_sites: unexpected answer from the server")
	}
	return sites, nil
}

func (c *call) findSite(name string) (restSite, error) { return c.findSiteIn(name, nil) }

// findSiteIn finds the named site in the account's listing, read with query.
func (c *call) findSiteIn(name string, query url.Values) (restSite, error) {
	sites, err := c.listSites(query)
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
	if c.caller.Mode == CallerModeEvents && c.owner == "" {
		return restSite{}, codedError("no_personal_sites", fmt.Sprintf("no site named %q: sites here belong to an event, so name the event as owner", name))
	}
	text := siteNotFoundText(name, names)
	if c.caller.Mode != CallerModeEvents {
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

// ownVisitsCaveat goes wherever the owner-excluded figures do.
const ownVisitsCaveat = " A device never used while signed in to Simple Host still counts as a person, and other people behind an address you were signed in from are left out while that mark lasts (30 days)."

const maxReadPaths = 10

// siteFile is one file read from a site version.
type siteFile struct {
	path      string
	size      int
	content   string
	binary    bool
	truncated bool
	ctype     string
	err       error // this file could not be read
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

// readFiles reads paths of a site version (0: the live one) in one request,
// returning the version read and each file, or that file's own refusal.
func readFiles(c *call, tool, site string, version int, paths []string) (int, []siteFile, error) {
	ref := "live"
	if version > 0 {
		ref = strconv.Itoa(version)
	}
	q := url.Values{}
	for _, path := range paths {
		q.Add("read", strings.TrimPrefix(path, "/"))
	}
	var body struct {
		Version int `json:"version_number"`
		Files   []struct {
			Size        int     `json:"size"`
			ContentType string  `json:"content_type"`
			Content     *string `json:"content"`
			Binary      bool    `json:"binary"`
			Truncated   bool    `json:"truncated"`
			Error       string  `json:"error"`
			Status      int     `json:"status"`
		} `json:"files"`
	}
	if err := c.doJSON(tool, http.MethodGet, sitePath(site, "/versions/"+ref+"/files?"+q.Encode()), nil, &body); err != nil {
		return 0, nil, err
	}
	if len(body.Files) != len(paths) {
		return 0, nil, errors.New(tool + ": unexpected answer from the server")
	}
	files := make([]siteFile, len(paths))
	for i, f := range body.Files {
		files[i] = siteFile{path: paths[i], size: f.Size, binary: f.Binary, truncated: f.Truncated, ctype: f.ContentType}
		if f.Content != nil {
			files[i].content = *f.Content
		}
		if f.Error != "" {
			refusal, _ := json.Marshal(map[string]string{"error": f.Error})
			files[i].err = restError(tool, upstreamResult{status: f.Status, body: refusal})
		}
	}
	return body.Version, files, nil
}

func readSiteFiles(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	paths, single, err := readPathsArg(args)
	if err != nil {
		return output{}, err
	}
	version, _, err := wholeNumber(args, "version", false, 1, math.MaxInt32)
	if err != nil {
		return output{}, err
	}
	version, files, err := readFiles(c, "read_site_file", name, version, paths)
	if err != nil {
		return output{}, err
	}
	if !single {
		return readManyFiles(name, version, files), nil
	}
	f := files[0]
	if f.err != nil {
		return output{}, f.err
	}
	out := f.structured()
	out["site"], out["version"] = name, version
	if f.binary {
		return output{Text: fmt.Sprintf("%s is a binary file (%s, %d bytes); its contents are not shown.", f.path, f.ctype, f.size), Structured: out}, nil
	}
	return output{Text: f.content, Structured: out}, nil
}

// readPathsArg reads path (one file) or paths (several).
func readPathsArg(args map[string]any) (paths []string, single bool, err error) {
	_, single = args["path"]
	raw, present := args["paths"]
	if !present {
		path, err := stringArg(args, "path")
		if err != nil {
			return nil, false, errors.New("path is required (or paths, for several files)")
		}
		return []string{path}, single, nil
	}
	if single {
		return nil, false, errors.New("give path or paths, not both")
	}
	list, ok := listArg(raw)
	if !ok || len(list) == 0 || len(list) > maxReadPaths {
		return nil, false, fmt.Errorf("paths must be a list of 1 to %d file paths", maxReadPaths)
	}
	for _, item := range list {
		p, ok := item.(string)
		if !ok || p == "" {
			return nil, false, errors.New("paths must hold file paths as strings")
		}
		paths = append(paths, p)
	}
	return paths, false, nil
}

// readManyFiles answers several files; one missing file does not hide the others.
func readManyFiles(name string, version int, read []siteFile) output {
	files := make([]any, 0, len(read))
	var text strings.Builder
	for _, f := range read {
		switch {
		case f.err != nil:
			files = append(files, map[string]any{"path": f.path, "error": f.err.Error()})
			fmt.Fprintf(&text, "=== %s ===\n%s\n", f.path, f.err.Error())
			continue
		case f.binary:
			fmt.Fprintf(&text, "=== %s ===\n(binary file, %s, %d bytes; contents not shown)\n", f.path, f.ctype, f.size)
		default:
			fmt.Fprintf(&text, "=== %s ===\n%s\n", f.path, f.content)
		}
		files = append(files, f.structured())
	}
	return output{Text: text.String(), Structured: map[string]any{"site": name, "version": version, "files": files}}
}
