package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// HackWebTools forwards website, icon and account preference actions to the
// hosted REST API. The REST handlers remain responsible for permissions and
// validation; custom event hosts never receive a credential from these tools.
func HackWebTools() []Tool {
	slug := map[string]any{"slug": str("Event name.")}
	generic := map[string]any{"type": "object", "additionalProperties": true}
	return []Tool{
		{
			Name: "hack_get_event_website", Title: "Read event website mode",
			Description: "Read whether the built-in or custom event website is live and whether a custom version exists. Organiser only.",
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
			Name: "hack_publish_event_website", Title: "Publish event website files",
			Description: "Publish a complete event website from inline files (include index.html, with optional files_base64 for assets) or one archive_base64 tar.gz. This replaces the custom site's files, keeps version history, and switches the event host to custom. Ask the organiser before publishing. Organiser only.",
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
			OutputSchema: generic, Annotations: writes(false, true, true),
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
		{
			Name: "hack_set_event_icon", Title: "Set event icon",
			Description:  "Set the event icon from base64 PNG, JPEG or WebP bytes. The server checks actual image type and its configured size limit. Ask before replacing the icon. Organiser only.",
			InputSchema:  object(map[string]any{"slug": slug["slug"], "content_type": map[string]any{"type": "string", "enum": []string{"image/png", "image/jpeg", "image/webp"}, "description": "The image's MIME type."}, "image_base64": str("Base64-encoded image bytes, without a data: prefix.")}, "slug", "content_type", "image_base64"),
			OutputSchema: generic, Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/icon")
				if err != nil {
					return output{}, err
				}
				typ, err := stringArg(args, "content_type")
				if err != nil {
					return output{}, err
				}
				encoded, err := stringArg(args, "image_base64")
				if err != nil {
					return output{}, err
				}
				if len(encoded) > base64.StdEncoding.EncodedLen(4<<20) {
					return output{}, errors.New("image_base64 exceeds the maximum allowed image size")
				}
				body, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					return output{}, errors.New("image_base64 must be valid base64 image bytes")
				}
				if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" {
					return output{}, errors.New("content_type must be image/png, image/jpeg or image/webp")
				}
				return hackWebJSON(c, "hack_set_event_icon", http.MethodPut, path, body, map[string]string{"Content-Type": typ})
			},
		},
		{
			Name: "hack_clear_event_icon", Title: "Clear event icon",
			Description: "Remove the uploaded event icon and restore its generated initial icon. Ask the organiser first. Organiser only.",
			InputSchema: object(slug, "slug"), OutputSchema: generic, Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/icon")
				if err != nil {
					return output{}, err
				}
				return hackWebJSON(c, "hack_clear_event_icon", http.MethodDelete, path, nil, nil)
			},
		},
		{
			Name: "hack_get_public_event", Title: "Read public event data",
			Description: "Read the credentialless public event feed, including eligible gallery, published results and closed vote ranking. No private codes or contact email are returned.",
			InputSchema: object(slug, "slug"), OutputSchema: generic, Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				path, err := hackWebPath(args, "/public")
				if err != nil {
					return output{}, err
				}
				return hackWebJSON(c, "hack_get_public_event", http.MethodGet, path, nil, nil)
			},
		},
		{
			Name: "hack_get_preferences", Title: "Read my Simple Hack preferences",
			Description: "Read this account's theme and completed organiser/judge walkthrough flags.",
			InputSchema: noArgs(), OutputSchema: generic, Annotations: readOnly(),
			run: func(c *call, _ map[string]any) (output, error) {
				return hackWebJSON(c, "hack_get_preferences", http.MethodGet, "/v1/hack/preferences", nil, nil)
			},
		},
		{
			Name: "hack_set_preferences", Title: "Set my Simple Hack preferences",
			Description:  "Set this account's theme (system, light or dark) and/or whether each walkthrough is complete. Sends only supplied fields.",
			InputSchema:  object(map[string]any{"theme": map[string]any{"type": "string", "enum": []string{"system", "light", "dark"}, "description": "Colour theme for the person's Simple Hack pages: system follows their device."}, "organiser_walkthrough_done": map[string]any{"type": "boolean", "description": "true marks the organiser walkthrough as seen, so it stops showing."}, "judge_walkthrough_done": map[string]any{"type": "boolean", "description": "true marks the judge walkthrough as seen, so it stops showing."}}),
			OutputSchema: generic, Annotations: writes(false, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				body := map[string]any{}
				if theme, ok := args["theme"]; ok {
					body["theme"] = theme
				}
				if done, ok := args["organiser_walkthrough_done"]; ok {
					body["organiser_walkthrough_done"] = done
				}
				if done, ok := args["judge_walkthrough_done"]; ok {
					body["judge_walkthrough_done"] = done
				}
				if len(body) == 0 {
					return output{}, errors.New("set at least one preference")
				}
				raw, err := json.Marshal(body)
				if err != nil {
					return output{}, err
				}
				return hackWebJSON(c, "hack_set_preferences", http.MethodPatch, "/v1/hack/preferences", raw, nil)
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
