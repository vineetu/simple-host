// famSites declares Hack website tools and shared web REST adapters.
package hackmcp

import (
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
	"net/url"
)

// hackWebTools forwards website actions to the
// hosted REST API. The REST handlers remain responsible for permissions and
// validation; custom event hosts never receive a credential from these tools.
func hackWebTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event name.")}
	generic := map[string]any{"type": "object", "additionalProperties": true}
	return declare([]decl{
		{
			Name: "hack_set_event_website_mode", Title: "Choose event website",
			Description:  "Switch the event host between builtin and custom. Publish the custom website first with the ordinary site tools (update_site with site set to organiser.website_site from hack_get_event and owner set to the event's name). Switching back preserves its files. Ask the organiser first. Organiser only.",
			InputSchema:  mcp.Object(map[string]any{"slug": slug["slug"], "mode": map[string]any{"type": "string", "enum": []string{"builtin", "custom"}, "description": "builtin: the event page Simple Hack renders itself; custom: the website the organiser published with the event website tools."}}, "slug", "mode"),
			OutputSchema: generic, Annotations: mcp.Writes(true, true, true),
			run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
				path, err := hackWebPath(args, "/website")
				if err != nil {
					return mcp.Output{}, err
				}
				mode, err := mcp.StringArg(args, "mode")
				if err != nil {
					return mcp.Output{}, err
				}
				raw, _ := json.Marshal(map[string]string{"mode": mode})
				return hackWebJSON(c, "hack_set_event_website_mode", http.MethodPatch, path, raw, nil)
			},
		},
	})
}

func hackWebPath(args map[string]any, suffix string) (string, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return "", err
	}
	return "/v1/hack/events/" + url.PathEscape(slug) + suffix, nil
}

func hackWebJSON(c *mcp.Call, name, method, path string, body []byte, extra map[string]string) (mcp.Output, error) {
	res := c.Do(method, path, body, extra)
	if !res.OK() {
		return mcp.Output{}, hackRestError(name, res)
	}
	var data map[string]any
	if err := json.Unmarshal(res.Body(), &data); err != nil || data == nil {
		return mcp.Output{}, fmt.Errorf("%s: server returned invalid JSON", name)
	}
	return mcp.Output{Text: mcp.JSONText(data), Structured: data}, nil
}
