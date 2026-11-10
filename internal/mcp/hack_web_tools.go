// famSites declares Hack website tools and shared web REST adapters.
package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// hackWebTools forwards website actions to the
// hosted REST API. The REST handlers remain responsible for permissions and
// validation; custom event hosts never receive a credential from these tools.
func hackWebTools() []Tool {
	slug := map[string]any{"slug": str("Event name.")}
	generic := map[string]any{"type": "object", "additionalProperties": true}
	return []Tool{
		{
			Name: "hack_get_event_website", Title: "Read event website mode",
			Description: "Read whether the built-in or custom event website is live and list published versions and upload limits. Organiser only.",
			InputSchema: object(slug, "slug"), OutputSchema: generic, Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/website")
				if err != nil {
					return output{}, err
				}
				return hackWebJSON(c, "hack_get_event_website", http.MethodGet, path, nil, nil)
			},
		},
		{
			policy: toolPolicy{analytics: analyticsPolicy{fileArguments: true}},
			Name:   "hack_publish_event_website", Title: "Publish event website files",
			Description: "Publish a complete event website from inline files (include index.html, with optional files_base64 for assets) or one archive_base64 tar.gz. This replaces the custom site's files, keeps version history, and switches the event host to custom. Ask the organiser before publishing. Organiser only. Build an event website from hack_get_page_recipe topic event: it reads the public event feed live, so the page updates itself at every stage, results included; never copy stage, announcements or results into the HTML.",
			InputSchema: object(map[string]any{
				"slug":           slug["slug"],
				"files":          map[string]any{"type": "object", "description": "Map of site-relative path to UTF-8 file contents.", "additionalProperties": map[string]any{"type": "string"}},
				"files_base64":   filesBase64Schema(),
				"archive_base64": str("Optional base64-encoded tar.gz archive; use instead of files and files_base64."),
			}, "slug"), OutputSchema: generic, Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/website")
				if err != nil {
					return output{}, err
				}
				files, err := stringMap(args, "files")
				if err != nil {
					return output{}, err
				}
				binary, err := stringMap(args, "files_base64")
				if err != nil {
					return output{}, err
				}
				archive, hasArchive, err := presentString(args, "archive_base64")
				if err != nil {
					return output{}, err
				}
				if hasArchive && (len(files) > 0 || len(binary) > 0) {
					return output{}, errors.New("archive_base64 cannot be combined with files or files_base64")
				}
				if !hasArchive {
					if _, ok := files["index.html"]; !ok {
						if _, ok := binary["index.html"]; !ok {
							return output{}, errors.New("files must include index.html at the site root")
						}
					}
				}
				state := c.do(http.MethodGet, path, nil, nil)
				if !state.ok() {
					return output{}, hackRestError("hack_publish_event_website", state)
				}
				var current struct {
					HasCustomPage bool `json:"has_custom_page"`
				}
				if err := json.Unmarshal(state.body, &current); err != nil {
					return output{}, errors.New("event website state could not be read")
				}
				var raw []byte
				var headers map[string]string
				if hasArchive {
					if len(archive) > int(c.server.cfg.MaxBodyBytes) {
						return output{}, errors.New("archive_base64 is too large for one connector request")
					}
					raw, err = base64.StdEncoding.DecodeString(archive)
					if err != nil {
						return output{}, errors.New("archive_base64 must be valid base64 tar.gz bytes")
					}
					headers = map[string]string{"Content-Type": "application/gzip"}
				} else {
					payload := map[string]any{"files": files}
					if len(binary) > 0 {
						payload["files_base64"] = binary
					}
					raw, err = json.Marshal(payload)
					if err != nil {
						return output{}, err
					}
					path += "/files"
				}
				if !current.HasCustomPage {
					path += "?create=1"
				}
				return hackWebJSON(c, "hack_publish_event_website", http.MethodPut, path, raw, headers)
			},
		},
		{
			Name: "hack_set_event_website_mode", Title: "Choose event website",
			Description:  "Switch the event host between builtin and custom. The custom website must have been published first; switching back preserves its files. Ask the organiser first. Organiser only.",
			InputSchema:  object(map[string]any{"slug": slug["slug"], "mode": map[string]any{"type": "string", "enum": []string{"builtin", "custom"}, "description": "builtin: the event page Simple Hack renders itself; custom: the website the organiser published with the event website tools."}}, "slug", "mode"),
			OutputSchema: generic, Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/website")
				if err != nil {
					return output{}, err
				}
				mode, err := stringArg(args, "mode")
				if err != nil {
					return output{}, err
				}
				raw, _ := json.Marshal(map[string]string{"mode": mode})
				return hackWebJSON(c, "hack_set_event_website_mode", http.MethodPatch, path, raw, nil)
			},
		},
	}
}

func hackWebPath(args map[string]any, suffix string) (string, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return "", err
	}
	return "/v1/hack/events/" + url.PathEscape(slug) + suffix, nil
}

func hackWebJSON(c *call, name, method, path string, body []byte, extra map[string]string) (output, error) {
	res := c.do(method, path, body, extra)
	if !res.ok() {
		return output{}, hackRestError(name, res)
	}
	var data map[string]any
	if err := json.Unmarshal(res.body, &data); err != nil || data == nil {
		return output{}, fmt.Errorf("%s: server returned invalid JSON", name)
	}
	return output{Text: jsonText(data), Structured: data}, nil
}
