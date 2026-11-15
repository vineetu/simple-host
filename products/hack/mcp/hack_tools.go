// Shared Simple Hack registration and REST helpers.
package hackmcp

import (
	"fmt"
	"github.com/vsriram/simple-host/internal/mcp"
	"strings"
)

// Tools returns every base tool this package declares, hidden ones included,
// in family order. The app-only screen tools are not among them: they are
// built per tool server (Contribution.Apps, hack_apps.go).
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

// presentString reports whether key was sent. An empty string is kept: an
// update uses it to clear a field. nil is not a string.
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
