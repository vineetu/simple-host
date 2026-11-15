// Shared Simple Hack registration and REST helpers.
package mcp

import (
	"fmt"
	"strings"
)

// HackTools returns the registered event declarations, including hidden tools.
func HackTools() []Tool {
	r := &toolRegistry{}
	r.addFamilies(editionHack, hackFamilies(), nil)
	return r.declaredTools(editionHack)
}

func eventSlug(args map[string]any) (string, error) {
	slug, err := stringArg(args, "slug")
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

func hackRestError(tool string, u upstreamResult) error {
	msg, code := upstreamMessage(u)
	switch code {
	case "invalid_name":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nEvent names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Correct the name and call again.", tool, u.status, msg, code)
	case "name_reserved":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nThat event name is reserved. Ask the person for a different name.", tool, u.status, msg, code)
	default:
		return restError(tool, u)
	}
}
