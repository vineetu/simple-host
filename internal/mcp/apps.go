package mcp

import (
	"embed"
	"encoding/json"
	"html"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const appMIME = "text/html;profile=mcp-app"

//go:embed apps/*.html apps/*.js apps/*.css
var appFiles embed.FS

type appScreen struct {
	ID, Flag, URI, Tool, Label, HTML string
	CSP                              map[string][]string
	Permissions                      map[string]any
}

// Add later screens here; shared bridge, stylesheet, gating, and serving stay common.
var appScreens = []appScreen{
	{
		ID: "dashboard", Flag: "MCP_APP_DASHBOARD", URI: "ui://simple-host/dashboard",
		Label: "Weekly overview", HTML: "my-sites.html",
		CSP: map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
	},
	{
		ID: "my-sites", Flag: "MCP_APP_MY_SITES", URI: "ui://simple-host/my-sites",
		Tool: "list_sites", Label: "My sites", HTML: "my-sites.html",
		CSP: map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
	},
	{
		ID: "photo-picker", Flag: "MCP_APP_PHOTO_PICKER", URI: "ui://simple-host/photo-picker",
		Tool: "create_upload_link", Label: "Photo picker", HTML: "photo-picker.html",
		CSP: map[string][]string{"connectDomains": {}, "resourceDomains": {"$origin"}, "frameDomains": {}, "baseUriDomains": {}},
	},
	{
		ID: "site-preview", Flag: "MCP_APP_SITE_PREVIEW", URI: "ui://simple-host/site-preview",
		Tool: "get_site", Label: "Site preview", HTML: "site-preview.html",
		CSP:         map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
		Permissions: map[string]any{"clipboardWrite": map[string]any{}},
	},
}

func (s *Server) mySitesEnabled(c Caller) bool {
	return s.fixed == nil && c.Mode == "" && appFlag(os.Getenv("MCP_APP_MY_SITES"), c.Handle)
}

func (s *Server) dashboardEnabled(c Caller) bool {
	return s.mySitesEnabled(c) && appFlag(os.Getenv("MCP_APP_DASHBOARD"), c.Handle)
}

func appFlag(value, handle string) bool {
	if value == "on" {
		return true
	}
	if handle == "" || !strings.HasPrefix(value, "accounts:") {
		return false
	}
	for _, allowed := range strings.Split(strings.TrimPrefix(value, "accounts:"), ",") {
		if strings.TrimSpace(allowed) == handle {
			return true
		}
	}
	return false
}

// Read flags on each request. Never mutate the cached tool inventories.
func (s *Server) enabledScreens(c Caller) []appScreen {
	if s.fixed != nil || c.Mode != "" {
		return nil
	}
	var screens []appScreen
	for _, screen := range appScreens {
		if appFlag(os.Getenv(screen.Flag), c.Handle) && (screen.ID != "dashboard" || s.dashboardEnabled(c)) {
			screens = append(screens, screen)
		}
	}
	return screens
}

func appToolMetadata(list []Tool, screens []appScreen) []Tool {
	out := append([]Tool(nil), list...)
	for i, tool := range out {
		for _, screen := range screens {
			if tool.Name == screen.Tool {
				meta := make(map[string]any, len(tool.Meta)+1)
				for key, value := range tool.Meta {
					meta[key] = value
				}
				meta["ui"] = map[string]any{"resourceUri": screen.URI}
				out[i].Meta = meta
				if screen.ID == "my-sites" && tool.OutputSchema != nil {
					out[i].OutputSchema = mySitesOutputSchema()
				}
				if screen.ID == "site-preview" && tool.OutputSchema != nil {
					out[i].OutputSchema = sitePreviewSchema(tool.OutputSchema)
				}
			}
		}
	}
	return out
}

func (s *Server) previewFrameDomains() []string {
	domains := []string{}
	content, _ := url.Parse(s.cfg.ContentOrigin)
	contentCovered := false
	for _, base := range s.cfg.SiteBases {
		base = strings.ToLower(strings.TrimSpace(base))
		if base == "" {
			continue
		}
		domains = append(domains, "https://*."+base)
		if content != nil && content.Port() == "" && strings.HasSuffix(content.Hostname(), "."+base) {
			contentCovered = true
		}
	}
	// Path-based boxes may serve sites on a separate, unrelated content host.
	// Never allow the dashboard origin as an exact frame destination.
	if !contentCovered && content != nil && content.Scheme == "https" && content.Host != "" && content.Host != s.cfg.APIHost {
		domains = append(domains, content.Scheme+"://"+content.Host)
	}
	return domains
}

func (s *Server) appMeta(screen appScreen) map[string]any {
	csp := make(map[string][]string, len(screen.CSP))
	for key, domains := range screen.CSP {
		csp[key] = []string{}
		for _, domain := range domains {
			csp[key] = append(csp[key], strings.ReplaceAll(domain, "$origin", "https://"+s.cfg.APIHost))
		}
	}
	if screen.ID == "site-preview" {
		csp["frameDomains"] = s.previewFrameDomains()
	}
	ui := map[string]any{
		"csp":           csp,
		"prefersBorder": true,
	}
	if screen.Permissions != nil {
		ui["permissions"] = screen.Permissions
	}
	return map[string]any{"ui": ui}
}

func (s *Server) appResources(c Caller) []any {
	resources := []any{}
	for _, screen := range s.enabledScreens(c) {
		resources = append(resources, map[string]any{"uri": screen.URI, "name": screen.Label, "mimeType": appMIME, "_meta": s.appMeta(screen)})
	}
	return resources
}

func appLimit(key string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func (s *Server) readApp(req request, caller Caller) response {
	var params struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(req.Params, &params) != nil {
		return failure(req.ID, codeInvalidParams, "invalid resource params", nil)
	}
	for _, screen := range s.enabledScreens(caller) {
		if params.URI != screen.URI {
			continue
		}
		asset := func(name string) string { b, _ := appFiles.ReadFile("apps/" + name); return string(b) }
		config, _ := json.Marshal(map[string]any{
			"assetOrigin":  "https://" + s.cfg.APIHost,
			"maxBodyBytes": s.cfg.MaxBodyBytes,
			"maxBytes":     min(appLimit("PHOTO_MAX_BYTES", 1000000), 1<<20),
			"maxDimension": appLimit("PHOTO_MAX_DIMENSION", 2048),
			"frameDomains": s.previewFrameDomains(),
		})
		bridge := asset("bridge.js")
		if screen.ID == "site-preview" {
			bridge += "\n" + asset("frames.js")
		}
		page := strings.NewReplacer(
			"__STYLE__", asset("shared.css"), "__BRIDGE__", bridge,
			"__SCREEN__", asset(strings.TrimSuffix(screen.HTML, ".html")+".js"), "__CONFIG__", string(config),
			"__DASHBOARD__", strconv.FormatBool(s.dashboardEnabled(caller)),
			"__ASSET_ORIGIN__", html.EscapeString("https://"+s.cfg.APIHost),
		).Replace(asset(screen.HTML))
		return result(req.ID, map[string]any{"contents": []any{map[string]any{
			"uri": screen.URI, "mimeType": appMIME, "text": page, "_meta": s.appMeta(screen),
		}}})
	}
	return failure(req.ID, codeInvalidParams, "resource not available", nil)
}
