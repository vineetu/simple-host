package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	appsites "github.com/vsriram/simple-host/host/sites"
)

func siteDeployTools(limits Limits) []Tool {
	return []Tool{
		{
			policy: toolPolicy{flatList: flatDeployTool, analytics: analyticsPolicy{fileArguments: true, structuredText: true, emailIdentity: true, emailChanges: true}},
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
			Description: "Publish a NEW website from files given inline, at its own address. Fails if this account already has a site of that name, so it never overwrites anything; to change an existing site use update_site. " +
				"When the person says \"my site\", \"our site\", or \"my salon site\", call list_sites first: if they already have that site, add to it with update_site instead of making a new one. " +
				"`index.html` is required. Use relative links only (`css/style.css`, never `/css/style.css`), because a site can be served under a path as well as at a root. " +
				"By default anyone with the returned URL can open it as soon as this returns; with access specific only the owner and named viewers can, from the first request. Ask the person who should open it. A root _redirects file holds redirect rules, is validated on publish and is never served. " + deployFormTip + deploySizeTips,
			InputSchema: object(map[string]any{
				"site":         str(siteDesc + " Pick a short, descriptive name."),
				"access":       map[string]any{"type": "string", "enum": []string{"anyone", "specific"}, "description": "Default anyone: anyone with the address. specific: only the owner and named viewers; everyone else sees a sign-in page, from the first request."},
				"viewers":      createViewersSchema(limits),
				"files":        filesSchema(),
				"files_base64": filesBase64Schema(),
			}, "site", "files"),
			// Publishes to the public web; creates only, so nothing is lost.
			Annotations: writes(false, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "create") },
		},
		{
			policy: toolPolicy{flatList: flatDeployTool, analytics: analyticsPolicy{secretText: true, fileArguments: true, edits: true}},
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
				"author":  map[string]any{"type": "string", "maxLength": appsites.MaxVersionAuthor, "description": "Who made this version, shown in list_versions; free text, for example a person's name."},
			}, "site"),
			// Overwrites what is live (destructive, even though rollback_site
			// can restore it) and publishes to the public web.
			Annotations: writes(true, false, true),
			run:         func(c *call, args map[string]any) (output, error) { return deploySite(c, args, "replace") },
		},
	}
}

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
		path, find, replace, err := readEdit(i, item)
		if err != nil {
			return nil, err
		}
		text, seen := texts[path]
		if !seen {
			if text, err = editableText(c, site, live.ActiveVersion, path, i); err != nil {
				return nil, err
			}
		}
		if texts[path], err = replaceOnce(text, find, replace, path, i); err != nil {
			return nil, err
		}
	}
	return texts, nil
}

// readEdit reads edits[i]: {path, find, replace}, all strings, find not empty.
func readEdit(i int, item any) (path, find, replace string, err error) {
	edit, ok := item.(map[string]any)
	if !ok {
		return "", "", "", fmt.Errorf("edits[%d] must be an object with path, find, and replace", i)
	}
	for key := range edit {
		if key != "path" && key != "find" && key != "replace" {
			return "", "", "", fmt.Errorf("edits[%d] has unexpected field %q; it takes path, find, and replace", i, key)
		}
	}
	path, _ = edit["path"].(string)
	find, _ = edit["find"].(string)
	replace, okReplace := edit["replace"].(string)
	if path == "" || find == "" || !okReplace {
		return "", "", "", fmt.Errorf("edits[%d] needs path, a non-empty find, and replace (all strings)", i)
	}
	return path, find, replace, nil
}

// editableText is a live text file's content, for edits[i].
func editableText(c *call, site string, version int, path string, i int) (string, error) {
	f, err := readFile(c, "update_site", site, version, path)
	if err != nil {
		return "", fmt.Errorf("edits[%d]: could not read %s: %w", i, path, err)
	}
	if f.binary || f.truncated {
		return "", fmt.Errorf("edits[%d]: %s is not a text file under 200 KB; send it whole in files", i, path)
	}
	return f.content, nil
}

func replaceOnce(text, find, replace, path string, i int) (string, error) {
	switch n := strings.Count(text, find); n {
	case 1:
		return strings.Replace(text, find, replace, 1), nil
	case 0:
		return "", fmt.Errorf("edits[%d]: the find text is not in %s, so nothing was published. Copy it exactly from read_site_file (spaces and line breaks count)", i, path)
	default:
		return "", fmt.Errorf("edits[%d]: the find text occurs %d times in %s, so nothing was published. Include more of the surrounding text so it occurs once", i, n, path)
	}
}

// deploySite publishes the given files. mode is "create" (a new site only,
// never overwriting) or "replace" (an existing site only).
func deploySite(c *call, args map[string]any, mode string) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	in, err := deployFiles(c, args, name, mode)
	if err != nil {
		return output{}, err
	}
	body, err := json.Marshal(deployPayload(args, in, mode))
	if err != nil {
		return output{}, err
	}
	query, publish, err := deployQuery(args, mode, in.keep)
	if err != nil {
		return output{}, err
	}
	method := http.MethodPut
	if mode == "create" {
		method = http.MethodPost
	}
	res := c.do(method, sitePath(name, "/files"+query), body, nil)
	if !res.ok() {
		return output{}, deployRefusal(res, mode, name)
	}
	var site restSite
	_ = json.Unmarshal(res.body, &site)
	out := site.summary()
	out["file_count"] = len(in.files) + len(in.binary)
	warnText := ""
	if warns := pageWarnings(c, name, in.files); len(warns) > 0 {
		out["page_warnings"] = warns
		warnText = "\nFix these before telling the person the page works:\n- " + strings.Join(warns, "\n- ")
	}
	if !publish {
		return output{Text: storedText(c, name, site, res.body, out) + warnText, Structured: out}, nil
	}
	return output{Text: publishedText(name, site, args, mode, in.edited) + warnText, Structured: out}, nil
}

// deployPayload is the JSON body of the files request.
func deployPayload(args map[string]any, in deployInput, mode string) map[string]any {
	payload := map[string]any{"files": in.files}
	if remove, exists := args["remove_files"]; exists {
		payload["remove_files"] = remove
	}
	if len(in.binary) > 0 {
		payload["files_base64"] = in.binary
	}
	if mode == "create" {
		for _, key := range []string{"access", "viewers"} {
			if value, ok := args[key]; ok {
				payload[key] = value
			}
		}
	}
	return payload
}

// storedText answers a publish: false update, adding its version and
// preview link to out.
func storedText(c *call, name string, site restSite, body []byte, out map[string]any) string {
	var stored struct {
		Version    int    `json:"unpublished_version"`
		PreviewURL string `json:"preview_url"`
	}
	_ = json.Unmarshal(body, &stored)
	out["unpublished_version"] = stored.Version
	text := fmt.Sprintf("Stored %s version %d without making it live; visitors still see version %d at %s.", name, stored.Version, site.ActiveVersion, site.liveURL())
	if stored.PreviewURL != "" {
		out["preview_url"] = stored.PreviewURL
		text += " Preview it (the link works for anyone who has it, for " + span(c.limits().PreviewLinkTTL) + "): " + stored.PreviewURL
	}
	return text + fmt.Sprintf("\nWhen the person is happy, rollback_site with version %d makes it live.", stored.Version)
}

func publishedText(name string, site restSite, args map[string]any, mode string, edited []string) string {
	text := fmt.Sprintf("Published a new version of %s (version %d). Live at %s", name, site.ActiveVersion, site.liveURL())
	if mode == "create" {
		text = fmt.Sprintf("Created %s (version %d). Live at %s", name, site.ActiveVersion, site.liveURL()) + createAccessText(site, args)
	}
	// Said in the text, not a new structured field: apps that cache the
	// output schema (ChatGPT keeps it from the day it connected) refuse fields
	// they do not know.
	if len(edited) > 0 {
		text += "\nEdited " + strings.Join(edited, ", ") + "; every other file was kept."
	}
	return text
}

// deployInput is what a deploy sends. keep is whether the other live files stay.
type deployInput struct {
	files, binary map[string]string
	edited        []string
	keep          bool
}

// deployFiles reads what a deploy sends: files, edits applied to the live
// version (update_site only), and files_base64.
func deployFiles(c *call, args map[string]any, name, mode string) (deployInput, error) {
	var in deployInput
	var err error
	if in.files, err = stringMap(args, "files"); err != nil {
		return in, err
	}
	if in.keep, err = boolArg(args, "keep_other_files", false); err != nil {
		return in, err
	}
	if in.keep && mode != "replace" {
		return in, errors.New("keep_other_files is only for update_site")
	}
	if raw, present := args["edits"]; present {
		if mode != "replace" {
			return in, errors.New("edits is only for update_site")
		}
		if err := in.addEdits(c, name, raw); err != nil {
			return in, err
		}
	}
	if _, exists := args["remove_files"]; exists && !in.keep {
		return in, errors.New("remove_files requires keep_other_files")
	}
	if len(in.files) == 0 && !in.keep {
		return in, errors.New("files is required: an object mapping each path to its contents, including index.html")
	}
	if in.binary, err = stringMap(args, "files_base64"); err != nil {
		return in, err
	}
	for _, path := range in.edited {
		if _, dup := in.binary[path]; dup {
			return in, fmt.Errorf("%s is in both files_base64 and edits; use one of them for a file", path)
		}
	}
	return in, nil
}

// addEdits applies edits to the live files and adds them to files. An edit
// changes part of the live site, so the rest is always kept.
func (in *deployInput) addEdits(c *call, name string, raw any) error {
	texts, err := applyEdits(c, name, raw)
	if err != nil {
		return err
	}
	if in.files == nil {
		in.files = map[string]string{}
	}
	for path, text := range texts {
		if _, dup := in.files[path]; dup {
			return fmt.Errorf("%s is in both files and edits; use one of them for a file", path)
		}
		in.files[path] = text
		in.edited = append(in.edited, path)
	}
	sort.Strings(in.edited)
	in.keep = true
	return nil
}

// deployQuery reads publish and author into the deploy request's query.
func deployQuery(args map[string]any, mode string, keep bool) (string, bool, error) {
	var query []string
	publish, err := boolArg(args, "publish", true)
	if err != nil {
		return "", false, err
	}
	if !publish {
		if mode == "create" {
			return "", false, errors.New("create_site always makes a new site live; publish: false is for update_site")
		}
		query = append(query, "publish=false")
	}
	if keep {
		query = append(query, "keep=1")
	}
	if raw, present := args["author"]; present && raw != nil {
		if mode != "replace" {
			return "", false, errors.New("author is only for update_site")
		}
		author, err := stringArg(args, "author")
		if err != nil {
			return "", false, err
		}
		if author = appsites.VersionAuthor(author); author != "" {
			query = append(query, "author="+url.QueryEscape(author))
		}
	}
	if len(query) == 0 {
		return "", publish, nil
	}
	return "?" + strings.Join(query, "&"), publish, nil
}

// deployRefusal words the refusals an agent can act on: no such site to
// update, a name held by Recently deleted, or a site that already exists.
func deployRefusal(res upstreamResult, mode, name string) error {
	_, refusalCode := upstreamMessage(res)
	switch {
	case res.status == http.StatusNotFound && mode == "replace":
		return fmt.Errorf("update_site failed: there is no site named %q in this account. Use create_site for a new site, or list_sites for existing names", name)
	case res.status == http.StatusConflict && mode == "create" && strings.Contains(string(res.body), `"recently_deleted":true`):
		return codedError("recently_deleted", fmt.Sprintf("create_site failed: a site named %q was deleted recently and is in Recently deleted, which keeps its name. Ask the person whether to bring it back with restore_site, or pick another name [recently_deleted]", name))
	case res.status == http.StatusConflict && mode == "create" && refusalCode == "site_exists":
		return fmt.Errorf("create_site failed: this account already has a site named %q, and create_site never overwrites. To change it, use update_site (read its files first); for a separate site, pick another name", name)
	case mode == "create":
		return restError("create_site", res)
	}
	return restError("update_site", res)
}

// createAccessText says who can open a site create_site just made.
func createAccessText(site restSite, args map[string]any) string {
	if site.Access != "specific" {
		return "\nAnyone with the address can open it."
	}
	seen := map[string]bool{}
	if viewers, ok := args["viewers"].([]any); ok {
		for _, v := range viewers {
			if email, ok := v.(string); ok {
				seen[strings.ToLower(strings.TrimSpace(email))] = true
			}
		}
	}
	switch len(seen) {
	case 0:
		return "\nOnly you can open it; everyone else sees a sign-in page."
	case 1:
		return "\nOnly you and 1 named viewer can open it; everyone else sees a sign-in page."
	}
	return fmt.Sprintf("\nOnly you and %d named viewers can open it; everyone else sees a sign-in page.", len(seen))
}

// deployFormTip sits on create_site and update_site because a small model
// writing a contact form reads these descriptions, not the instructions.
const deployFormTip = "A form visitors send (contact, quote, booking, sign-up, order) must save to storage: call get_page_recipe first; never use a mailto: link or an outside form service. "

// deployPhotoTip: Simple Hack has no photos link and swaps in hackPhotoTip.
const deployPhotoTip = "For the person's own photos, give them create_upload_link's photos_url (it shrinks them); never ask them to resize or attach photos."

const deploySizeTips = "Check who_am_i for account file usage and list_sites for site sizes before deploying. Hosted Simple Host keeps 4 versions; new sites have a 200 MB whole-file allowance and accounts 1 GB (operator exceptions apply). The separate live copy and every kept version count; saved data and storage resources do not. " + deployPhotoTip + " Keep zip files, installers and videos elsewhere and link to them. Remove unused files; ask the person which old sites they no longer need."

func createViewersSchema(limits Limits) map[string]any {
	schema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Emails exactly as the person gave them; only with access specific. Added in the same create call."}
	if limits.SiteViewersMax > 0 {
		schema["maxItems"] = limits.SiteViewersMax
	}
	return schema
}
