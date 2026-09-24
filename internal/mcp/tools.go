package mcp

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
	"unicode/utf8"
)

// Tool is one callable operation. run turns validated arguments into REST
// requests (through call.do) and shapes the answer; it holds no business rule
// of its own.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`

	run func(c *call, args map[string]any) (output, error)
}

type output struct {
	Text       string
	Structured map[string]any
}

// call is one tools/call in flight: the original request (for the client
// address the rate limiters key on) and who it acts as.
type call struct {
	server *Server
	orig   *http.Request
	caller Caller
	handle string
}

// upstreamResult is one in-process REST answer.
type upstreamResult struct {
	status int
	header http.Header
	body   []byte
}

func (u upstreamResult) ok() bool { return u.status >= 200 && u.status < 300 }

// do serves one REST request into the application router as the caller.
func (c *call) do(method, path string, body []byte, extra map[string]string) upstreamResult {
	req, err := http.NewRequestWithContext(c.orig.Context(), method, path, bytes.NewReader(body))
	if err != nil {
		return upstreamResult{status: http.StatusInternalServerError, body: []byte(`{"error":"could not build request"}`)}
	}
	req.Host = c.server.cfg.APIHost
	req.Header.Set("X-API-Key", c.caller.APIKey)
	req.Header.Set("X-Skill-Version", c.server.cfg.SkillVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	// The per-IP limiters must see the real client, or every connector call
	// would share one bucket (and escape the limit the REST caller meets).
	req.RemoteAddr = c.orig.RemoteAddr
	if xff := c.orig.Header.Get("X-Forwarded-For"); xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := &capture{header: http.Header{}, status: http.StatusOK}
	c.server.cfg.Upstream.ServeHTTP(rec, req)
	return upstreamResult{status: rec.status, header: rec.header, body: rec.body.Bytes()}
}

// siteData serves a request to the caller's own site's state or collections.
// Those routes are addressed by handle and Origin-gated; the shared content
// host is an origin every site accepts.
func (c *call) siteData(method, site, suffix string, body []byte, extra map[string]string) (upstreamResult, error) {
	handle, err := c.ownHandle()
	if err != nil {
		return upstreamResult{}, err
	}
	if extra == nil {
		extra = map[string]string{}
	}
	extra["Origin"] = c.server.cfg.ContentOrigin
	path := "/v1/u/" + url.PathEscape(handle) + "/sites/" + url.PathEscape(site) + suffix
	return c.do(method, path, body, extra), nil
}

// ownHandle is the caller's URL handle, read once per call. An account gets a
// handle on its first deploy, so a brand-new one may not have it yet.
func (c *call) ownHandle() (string, error) {
	if c.handle != "" {
		return c.handle, nil
	}
	me := c.do(http.MethodGet, "/v1/me", nil, nil)
	if !me.ok() {
		return "", restError("who_am_i", me)
	}
	var body struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(me.body, &body)
	if body.Handle == "" {
		return "", errors.New("this account has no sites yet, so it has no site data to read or change; deploy a site first with deploy_site")
	}
	c.handle = body.Handle
	return c.handle, nil
}

// capture is a minimal buffering ResponseWriter.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(status int) {
	if c.wrote {
		return
	}
	c.status, c.wrote = status, true
}
func (c *capture) Write(p []byte) (int, error) {
	c.wrote = true
	return c.body.Write(p)
}

// restError turns a refused REST call into a message a model can act on: the
// server's own error, plus what to do about it.
func restError(tool string, u upstreamResult) error {
	var payload struct {
		Error  string `json:"error"`
		Code   string `json:"code"`
		Domain string `json:"domain"`
	}
	_ = json.Unmarshal(u.body, &payload)
	msg := payload.Error
	if msg == "" {
		msg = strings.TrimSpace(string(u.body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	if msg == "" {
		msg = http.StatusText(u.status)
	}
	hint := ""
	switch {
	case u.status == http.StatusNotFound && strings.Contains(msg, "site not found"):
		hint = "No site of that name in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here."
	case u.status == http.StatusNotFound:
		hint = "Check the name against list_sites (and version numbers against list_versions)."
	case u.status == http.StatusConflict:
		hint = "A site of that name already exists in this account. Use mode \"replace\" to publish a new version of it, or pick another name."
	case u.status == http.StatusBadRequest:
		hint = "The request was rejected as invalid; correct the arguments rather than retrying the same call."
	case u.status == http.StatusRequestEntityTooLarge:
		hint = "Too large. Send fewer or smaller files."
	case u.status == http.StatusPreconditionFailed:
		hint = "The data changed since you read it. Call get_state again and redo the change on the fresh copy."
	case u.status == http.StatusTooManyRequests:
		hint = "Rate limited. Wait a minute before trying again; do not retry in a loop."
	case u.status == http.StatusUnauthorized:
		hint = "The connection to Simple Host is no longer signed in. Ask the person to reconnect Simple Host in their app's connector settings."
	case u.status == http.StatusForbidden:
		hint = "This account is not allowed to do that."
	}
	out := fmt.Sprintf("%s failed (HTTP %d): %s", tool, u.status, msg)
	if payload.Code != "" {
		out += " [" + payload.Code + "]"
	}
	if hint != "" {
		out += "\n" + hint
	}
	return errors.New(out)
}

// ---- argument helpers -------------------------------------------------------

func object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func noArgs() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func readOnly() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

func writes(destructive, idempotent bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": true}
}

func stringArg(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
}

func optionalString(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	return strings.TrimSpace(value), nil
}

// wholeNumber reads an integer argument, refusing fractions rather than
// truncating them (version 1.9 must not quietly mean version 1).
func wholeNumber(args map[string]any, key string, required bool, min, max int) (int, bool, error) {
	raw, present := args[key]
	if !present || raw == nil {
		if required {
			return 0, false, fmt.Errorf("%s is required", key)
		}
		return 0, false, nil
	}
	var value float64
	switch v := raw.(type) {
	case float64:
		value = v
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(v, "v")), 64)
		if err != nil {
			return 0, false, fmt.Errorf("%s must be a number, got %q", key, v)
		}
		value = f
	default:
		return 0, false, fmt.Errorf("%s must be a number, got %T", key, raw)
	}
	if value != math.Trunc(value) {
		return 0, false, fmt.Errorf("%s must be a whole number, got %v", key, value)
	}
	if value < float64(min) || value > float64(max) {
		return 0, false, fmt.Errorf("%s must be between %d and %d, got %v", key, min, max, value)
	}
	return int(value), true, nil
}

// siteArg reads and shape-checks a site name. The REST layer validates it
// again; checking here turns a refusal into a precise message.
func siteArg(args map[string]any) (string, error) {
	site, err := stringArg(args, "site")
	if err != nil {
		return "", err
	}
	site = strings.ToLower(site)
	for _, r := range site {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return "", fmt.Errorf("site %q is not a valid site name: use lowercase letters, numbers and hyphens only (e.g. \"birthday-rsvp\")", site)
		}
	}
	return site, nil
}

func stringMap(args map[string]any, key string) (map[string]string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object mapping file paths to contents", key)
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%q] must be a string, got %T", key, k, v)
		}
		out[k] = s
	}
	return out, nil
}

// ---- site helpers -----------------------------------------------------------

// restSite is the subset of the REST site object the tools report.
type restSite struct {
	Name          string `json:"name"`
	ActiveVersion int    `json:"active_version"`
	SiteURL       string `json:"site_url"`
	UpdatedAt     string `json:"updated_at"`
	CustomDomain  string `json:"custom_domain"`
	DomainStatus  string `json:"domain_status"`
	Visibility    string `json:"visibility"`
}

// liveURL is the one address to give people: a connected, working custom
// domain wins, because the shared-host address redirects there.
func (s restSite) liveURL() string {
	if s.CustomDomain != "" && s.DomainStatus == "active" {
		return "https://" + s.CustomDomain + "/"
	}
	return s.SiteURL
}

func (s restSite) summary() map[string]any {
	m := map[string]any{
		"name":           s.Name,
		"url":            s.liveURL(),
		"active_version": s.ActiveVersion,
		"listed":         s.Visibility == "public",
	}
	if s.UpdatedAt != "" {
		m["updated_at"] = s.UpdatedAt
	}
	if s.CustomDomain != "" {
		m["custom_domain"] = s.CustomDomain
		m["domain_status"] = s.DomainStatus
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
	for _, s := range sites {
		if s.Name == name {
			return s, nil
		}
	}
	return restSite{}, fmt.Errorf("no site named %q in this account. Call list_sites for the exact names; a site owned by someone else cannot be changed from here", name)
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// ---- the tools ---------------------------------------------------------------

const siteDesc = "The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens."

// maxFileText bounds how much of one file read_site_file hands back, so one
// call cannot flood the conversation.
const maxFileText = 200 << 10

// Tools is the callable surface, in the order a first-time agent needs it.
func Tools() []Tool {
	return []Tool{
		{
			Name:        "who_am_i",
			Title:       "Who am I signed in as",
			Description: "Return the Simple Host account this connection acts as: its email, its handle (the part of site addresses after sites.simple-host.app/) and its public page listing its sites.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				res := c.do(http.MethodGet, "/v1/me", nil, nil)
				if !res.ok() {
					return output{}, restError("who_am_i", res)
				}
				var me struct {
					Username    string `json:"username"`
					Handle      string `json:"handle"`
					DisplayName string `json:"display_name"`
				}
				_ = json.Unmarshal(res.body, &me)
				out := map[string]any{"email": me.Username}
				text := "Signed in to Simple Host as " + me.Username + "."
				if me.Handle != "" {
					page := c.server.cfg.ContentOrigin + "/" + me.Handle
					out["handle"], out["public_page"] = me.Handle, page
					text += " Handle: " + me.Handle + ". Public page: " + page
				}
				if me.DisplayName != "" {
					out["display_name"] = me.DisplayName
				}
				return output{Text: text, Structured: out}, nil
			},
		},
		{
			Name:        "list_sites",
			Title:       "List my sites",
			Description: "List every site in this account with its live address, active version and whether it has its own domain. Call this before changing a site to get its exact name.",
			InputSchema: noArgs(),
			Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				sites, err := c.listSites()
				if err != nil {
					return output{}, err
				}
				items := make([]any, 0, len(sites))
				for _, s := range sites {
					items = append(items, s.summary())
				}
				out := map[string]any{"sites": items, "count": len(items)}
				if len(items) == 0 {
					return output{Text: "This account has no sites yet. Publish one with deploy_site.", Structured: out}, nil
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "get_site",
			Title: "Get one site and its files",
			Description: "Show one site: its live address, active version, custom domain, and the list of files in the live version with their sizes. " +
				"Call this before editing an existing site, then read_site_file for each file you will change.",
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
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "read_site_file",
			Title: "Read a file from a site",
			Description: "Return the contents of one file of a site (the live version unless `version` is given). Use it to edit an existing site: read the file, change it, and deploy the full set of files again. " +
				"Text files come back as text; binary files (images, fonts) are only described, since deploy_site keeps nothing you do not resend — resend a binary file only if you have its bytes.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"path":    str("Path of the file from the site root, e.g. `index.html` or `css/style.css`."),
				"version": map[string]any{"type": "integer", "description": "Version number to read from. Defaults to the live version."},
			}, "site", "path"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				path, err := stringArg(args, "path")
				if err != nil {
					return output{}, err
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
				segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
				for i, s := range segments {
					segments[i] = url.PathEscape(s)
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions/"+strconv.Itoa(version)+"/files/"+strings.Join(segments, "/"), nil, nil)
				if !res.ok() {
					return output{}, restError("read_site_file", res)
				}
				out := map[string]any{"site": name, "path": path, "version": version, "size": len(res.body)}
				ctype := res.header.Get("Content-Type")
				textual := utf8.Valid(res.body) && !bytes.ContainsRune(res.body, 0) &&
					!strings.HasPrefix(ctype, "image/") && !strings.HasPrefix(ctype, "font/") && !strings.HasPrefix(ctype, "audio/") && !strings.HasPrefix(ctype, "video/")
				if !textual {
					out["binary"] = true
					return output{Text: fmt.Sprintf("%s is a binary file (%s, %d bytes); its contents are not shown.", path, ctype, len(res.body)), Structured: out}, nil
				}
				content := string(res.body)
				if len(content) > maxFileText {
					content = content[:maxFileText]
					for !utf8.ValidString(content) {
						content = content[:len(content)-1]
					}
					out["truncated"] = true
				}
				out["content"] = content
				return output{Text: content, Structured: out}, nil
			},
		},
		{
			Name:  "deploy_site",
			Title: "Publish a site",
			Description: "Publish a website from files given inline. Creates the site, or publishes a new version of an existing one. " +
				"The files you send are the COMPLETE new version: anything not included stops existing on the live site, so to change one page of an existing site, read the others with read_site_file and send them all again. " +
				"`index.html` is required. Use relative links only (`css/style.css`, never `/css/style.css`), because sites live under a path. " +
				"Earlier versions are kept and can be restored with rollback_site. The site is public at the returned URL as soon as this returns.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc + " New sites: pick a short, descriptive name."),
				"files": map[string]any{
					"type":                 "object",
					"description":          "Map of file path → text content, e.g. {\"index.html\": \"<!DOCTYPE html>…\", \"css/style.css\": \"body{…}\"}. Paths are relative, no leading slash, no `..`.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"files_base64": map[string]any{
					"type":                 "object",
					"description":          "Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"mode": map[string]any{
					"type": "string",
					"enum": []string{"auto", "create", "replace"},
					"description": "`create` only makes a new site and fails if the name is taken; `replace` only updates a site that exists; `auto` (default) does whichever applies. " +
						"Use `create` when the person asked for a new site, so an existing site is never overwritten by accident.",
				},
			}, "site", "files"),
			Annotations: writes(false, false),
			run:         deploySite,
		},
		{
			Name:        "list_versions",
			Title:       "List a site's versions",
			Description: "List the versions kept for a site, newest first, with which one is live. Call this before rollback_site.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/versions", nil, nil)
				if !res.ok() {
					return output{}, restError("list_versions", res)
				}
				var versions []map[string]any
				_ = json.Unmarshal(res.body, &versions)
				sort.SliceStable(versions, func(i, j int) bool {
					a, _ := versions[i]["version_number"].(float64)
					b, _ := versions[j]["version_number"].(float64)
					return a > b
				})
				out := map[string]any{"site": name, "versions": versions}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "rollback_site",
			Title:       "Make an earlier version live",
			Description: "Make one of a site's earlier versions live again (visitors see it immediately). Nothing is deleted; the current version stays available and can be restored the same way. Confirm the version with the person first.",
			InputSchema: object(map[string]any{
				"site":    str(siteDesc),
				"version": map[string]any{"type": "integer", "description": "The version number to make live, from list_versions."},
			}, "site", "version"),
			Annotations: writes(false, true),
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
			Name:  "delete_site",
			Title: "Delete a site permanently",
			Description: "DESTRUCTIVE AND IRREVERSIBLE: deletes the site, every version of it, and all of its saved data (state and collections). The address stops working. " +
				"Only call this after the person has explicitly confirmed, in this conversation, that they want this specific site deleted. Pass the site name twice: as `site` and as `confirm_name`.",
			InputSchema: object(map[string]any{
				"site":         str(siteDesc),
				"confirm_name": str("The same site name again, typed out, as confirmation."),
			}, "site", "confirm_name"),
			Annotations: writes(true, true),
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
				return output{Text: "Deleted " + name + " with all its versions and data.", Structured: map[string]any{"deleted": name}}, nil
			},
		},
		{
			Name:        "rename_site",
			Title:       "Rename a site",
			Description: "Give a site a new name, which changes its address. The old address stops working and is not redirected, so tell the person. A connected custom domain stays attached.",
			InputSchema: object(map[string]any{
				"site":     str(siteDesc),
				"new_name": str("The new site name: lowercase letters, numbers and hyphens."),
			}, "site", "new_name"),
			Annotations: writes(false, false),
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
			Name:  "set_visibility",
			Title: "Show or hide a site on my public page",
			Description: "Choose whether a site is listed on the account's public page (sites.simple-host.app/<handle>). " +
				"This is NOT privacy: an unlisted site is still public to anyone with its address. Simple Host has no private or password-protected sites; never describe unlisted as private.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"visibility": map[string]any{"type": "string", "enum": []string{"public", "unlisted"}, "description": "`public` lists it on the public page; `unlisted` leaves it off (still reachable by its address)."},
			}, "site", "visibility"),
			Annotations: writes(false, true),
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
			Name:        "get_state",
			Title:       "Read a site's saved state",
			Description: "Read a site's shared JSON state document (what its pages save with SH.state / PATCH state), with its etag. Anyone can read this data; it is public.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res, err := c.siteData(http.MethodGet, name, "/state", nil, nil)
				if err != nil {
					return output{}, err
				}
				if !res.ok() {
					return output{}, restError("get_state", res)
				}
				var state any
				_ = json.Unmarshal(res.body, &state)
				out := map[string]any{"site": name, "etag": res.header.Get("ETag"), "state": state}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "update_state",
			Title: "Change a site's saved state",
			Description: "Change a site's shared JSON state. Prefer `ops` (atomic, safe with visitors saving at the same time): " +
				"{op:\"set\",path:\"a.b\",value:…}, {op:\"inc\",path:\"count\",by:1}, {op:\"append\",path:\"items\",value:…}, {op:\"remove\",path:\"a.b\"}, {op:\"removeWhere\",path:\"items\",match:{id:\"x\"}}. " +
				"Or send `replace` with a whole new document (pass `if_match` with the etag from get_state so a concurrent change is not overwritten). The document is capped at about 1 MB and is public.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"ops": map[string]any{
					"type":        "array",
					"description": "Atomic operations applied in order.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"op":    map[string]any{"type": "string", "enum": []string{"set", "inc", "append", "remove", "removeWhere"}},
							"path":  map[string]any{"type": "string", "description": "Dot path, e.g. `rsvps` or `totals.yes`."},
							"value": map[string]any{"description": "For set and append."},
							"by":    map[string]any{"type": "number", "description": "For inc."},
							"match": map[string]any{"type": "object", "description": "For removeWhere: fields an array element must match."},
						},
						"required": []string{"op", "path"},
					},
				},
				"replace":  map[string]any{"type": "object", "description": "A whole new state document. Use instead of ops."},
				"if_match": str("Only with replace: the etag from get_state."),
			}, "site"),
			Annotations: writes(false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				ops, hasOps := args["ops"]
				replace, hasReplace := args["replace"]
				if hasOps == hasReplace {
					return output{}, errors.New("send exactly one of ops or replace")
				}
				var res upstreamResult
				if hasOps {
					if _, ok := ops.([]any); !ok {
						return output{}, errors.New("ops must be an array of operations")
					}
					body, _ := json.Marshal(map[string]any{"ops": ops})
					res, err = c.siteData(http.MethodPatch, name, "/state", body, nil)
				} else {
					ifMatch, ierr := optionalString(args, "if_match")
					if ierr != nil {
						return output{}, ierr
					}
					body, _ := json.Marshal(replace)
					extra := map[string]string{}
					if ifMatch != "" {
						extra["If-Match"] = ifMatch
					}
					res, err = c.siteData(http.MethodPut, name, "/state", body, extra)
				}
				if err != nil {
					return output{}, err
				}
				if !res.ok() {
					return output{}, restError("update_state", res)
				}
				var state any
				_ = json.Unmarshal(res.body, &state)
				out := map[string]any{"site": name, "etag": res.header.Get("ETag"), "state": state}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "list_collections",
			Title:       "List a site's collections",
			Description: "List the append-only collections a site has saved into (sign-ups, RSVPs, messages…) with how many items each holds.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/collections", nil, nil)
				if !res.ok() {
					return output{}, restError("list_collections", res)
				}
				var parsed any
				_ = json.Unmarshal(res.body, &parsed)
				out := map[string]any{"site": name}
				switch v := parsed.(type) {
				case map[string]any:
					for k, val := range v {
						if k != "_notice" {
							out[k] = val
						}
					}
				case []any:
					out["collections"] = v
				}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "read_collection",
			Title:       "Read a site's collection",
			Description: "Read items a site's pages have saved into a collection, newest first. Pass `before` with the returned `next` to page back. This data is public.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"collection": str("Collection name, e.g. `rsvps`."),
				"limit":      map[string]any{"type": "integer", "description": "How many items (1–200, default 50)."},
				"before":     str("Cursor from a previous call's `next`, to read older items."),
			}, "site", "collection"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				coll, err := stringArg(args, "collection")
				if err != nil {
					return output{}, err
				}
				limit, given, err := wholeNumber(args, "limit", false, 1, 200)
				if err != nil {
					return output{}, err
				}
				if !given {
					limit = 50
				}
				before, err := optionalString(args, "before")
				if err != nil {
					return output{}, err
				}
				q := url.Values{"limit": {strconv.Itoa(limit)}}
				if before != "" {
					q.Set("before", before)
				}
				res, err := c.siteData(http.MethodGet, name, "/collections/"+url.PathEscape(coll)+"?"+q.Encode(), nil, nil)
				if err != nil {
					return output{}, err
				}
				if !res.ok() {
					return output{}, restError("read_collection", res)
				}
				var out map[string]any
				if err := json.Unmarshal(res.body, &out); err != nil || out == nil {
					out = map[string]any{}
				}
				out["site"], out["collection"] = name, coll
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "add_to_collection",
			Title:       "Add an item to a collection",
			Description: "Append one JSON object to a site's collection (at most 64 KB), exactly as a page would. Appends are never undone, so do not retry one that may have succeeded.",
			InputSchema: object(map[string]any{
				"site":       str(siteDesc),
				"collection": str("Collection name, e.g. `rsvps`."),
				"item":       map[string]any{"type": "object", "description": "The item to append."},
			}, "site", "collection", "item"),
			Annotations: writes(false, false),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				coll, err := stringArg(args, "collection")
				if err != nil {
					return output{}, err
				}
				item, ok := args["item"].(map[string]any)
				if !ok {
					return output{}, errors.New("item must be a JSON object")
				}
				body, _ := json.Marshal(item)
				res, err := c.siteData(http.MethodPost, name, "/collections/"+url.PathEscape(coll), body, nil)
				if err != nil {
					return output{}, err
				}
				if !res.ok() {
					return output{}, restError("add_to_collection", res)
				}
				var out map[string]any
				_ = json.Unmarshal(res.body, &out)
				return output{Text: "Added to " + coll + ".", Structured: out}, nil
			},
		},
		{
			Name:  "connect_domain",
			Title: "Connect a custom domain",
			Description: "Start connecting the person's own domain (e.g. `rsvp.example.com` or `example.com`) to a site. Returns the one DNS record they must add at their domain registrar; relay it exactly. " +
				"Then check with domain_status until it is active. A domain also turns on visitor sign-in for saves on that site. Once active the site lives only at the domain.",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc),
				"domain": str("The domain or subdomain, without https://, e.g. `rsvp.example.com`."),
			}, "site", "domain"),
			Annotations: writes(false, true),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				domain, err := stringArg(args, "domain")
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"domain": domain})
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/domain", body, nil)
				if !res.ok() {
					return output{}, restError("connect_domain", res)
				}
				var out map[string]any
				_ = json.Unmarshal(res.body, &out)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "domain_status",
			Title:       "Check a custom domain",
			Description: "Check whether a site's custom domain is connected: pending (the DNS record is not seen yet) or active.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/domain", nil, nil)
				if !res.ok() {
					return output{}, restError("domain_status", res)
				}
				var out map[string]any
				_ = json.Unmarshal(res.body, &out)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:        "site_analytics",
			Title:       "How many people visited",
			Description: "Visits to a site over the last N days, split into people, bots and monitoring. When asked how many people visited, report the `person` numbers only.",
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
				out := map[string]any{"site": name, "range_days": full["range_days"], "totals": full["totals"], "last_24h": full["last_24h"]}
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
	}
}

func deploySite(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	files, err := stringMap(args, "files")
	if err != nil {
		return output{}, err
	}
	if len(files) == 0 {
		return output{}, errors.New("files is required: an object mapping each path to its contents, including index.html")
	}
	binary, err := stringMap(args, "files_base64")
	if err != nil {
		return output{}, err
	}
	if _, ok := files["index.html"]; !ok {
		if _, ok := binary["index.html"]; !ok {
			return output{}, errors.New("files must include index.html at the site root")
		}
	}
	mode, err := optionalString(args, "mode")
	if err != nil {
		return output{}, err
	}
	switch mode {
	case "":
		mode = "auto"
	case "auto", "create", "replace":
	default:
		return output{}, fmt.Errorf("mode must be auto, create or replace, got %q", mode)
	}
	payload := map[string]any{"files": files}
	if len(binary) > 0 {
		payload["files_base64"] = binary
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(name) + "/files"

	var res upstreamResult
	created := false
	switch mode {
	case "create":
		res, created = c.do(http.MethodPost, path, body, nil), true
	case "replace":
		res = c.do(http.MethodPut, path, body, nil)
	default:
		res, created = c.do(http.MethodPost, path, body, nil), true
		if res.status == http.StatusConflict {
			res, created = c.do(http.MethodPut, path, body, nil), false
		}
	}
	if !res.ok() {
		if res.status == http.StatusNotFound && mode == "replace" {
			return output{}, fmt.Errorf("deploy_site failed: there is no site named %q in this account to replace. Use mode \"create\" for a new site, or list_sites for existing names", name)
		}
		return output{}, restError("deploy_site", res)
	}
	var site restSite
	_ = json.Unmarshal(res.body, &site)
	out := site.summary()
	out["created"] = created
	out["file_count"] = len(files) + len(binary)
	verb := "Published a new version of"
	if created {
		verb = "Created"
	}
	text := fmt.Sprintf("%s %s (version %d). Live at %s", verb, name, site.ActiveVersion, site.liveURL())
	return output{Text: text, Structured: out}, nil
}
