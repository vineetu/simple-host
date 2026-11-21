package hackmcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

// Tools returns every base tool this package declares, hidden ones included, in
// family order. App-only screen tools are built per tool server (hack_apps.go).
func Tools() []mcp.Tool {
	var tools []mcp.Tool
	for _, family := range Contribution().Families {
		tools = append(tools, family.Tools...)
	}
	return tools
}

func eventSlug(args map[string]any) (string, error) {
	slug, err := mcp.StringArg(args, "slug")
	if err != nil {
		return "", err
	}
	return strings.ToLower(slug), nil
}

// presentString reports whether key was sent; an empty string counts (an update
// uses it to clear a field).
func presentString(args map[string]any, key string) (string, bool, error) {
	raw, present := args[key]
	if !present {
		return "", false, nil
	}
	if raw == nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	return strings.TrimSpace(value), true, nil
}

func hackRestError(tool string, u mcp.UpstreamResult) error {
	msg, code := mcp.UpstreamMessage(u)
	switch code {
	case "invalid_name":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nEvent names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Correct the name and call again.", tool, u.Status(), msg, code)
	case "name_reserved":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nThat event name is reserved. Ask the person for a different name.", tool, u.Status(), msg, code)
	default:
		return mcp.RestError(tool, u)
	}
}

func hackDo(c *mcp.Call, tool, method, path string, body any) ([]byte, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	res := c.Do(method, path, raw, nil)
	if !res.OK() {
		return nil, hackRestError(tool, res)
	}
	return res.Body(), nil
}

func eventPath(slug, suffix string) string {
	return "/v1/hack/events/" + url.PathEscape(slug) + suffix
}

func withFields(base, fields map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(fields))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range fields {
		out[k] = v
	}
	return out
}
