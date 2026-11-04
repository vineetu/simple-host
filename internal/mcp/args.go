package mcp

// Argument schemas and parsers shared by tool families.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

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

// Annotations follow the plugin directory's definitions, and every tool sets
// all three hints explicitly (a missing hint is a review failure):
//
//   - readOnlyHint: true only when the tool fetches and changes nothing.
//   - openWorldHint: true when the tool publishes to the public internet or
//     reaches an open-ended outside party (an arbitrary domain). Reading the
//     person's own account is a bounded workspace: false.
//   - destructiveHint: true when the tool can delete or overwrite something,
//     in any mode or through its defaults, even if a copy can be restored.
//
// The justification for each tool's values is in openai-plugin/SUBMISSION.md;
// keep the two in step.

// readOnly is a lookup in the person's own account.
func readOnly() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
}

// writes is a change. public says whether it puts something in front of the
// public (publishes, relists, re-addresses) or reaches an outside party.
func writes(destructive, idempotent, public bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": public}
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
			text := fmt.Sprintf("site %q is not a valid site name: use lowercase letters, numbers and hyphens only (e.g. \"birthday-rsvp\")", site)
			if guess := siteNameFromAddress(site); guess != "" {
				text = fmt.Sprintf("site %q is an address, not a site name: pass just the name (here probably %q); list_sites shows the exact names", site, guess)
			}
			return "", codedError("invalid_site_name", text)
		}
	}
	return site, nil
}

// siteNameFromAddress guesses the site name in an address such as
// https://birthday-rsvp.ann.simple-host.app/ (its first label).
func siteNameFromAddress(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	host, _, _ := strings.Cut(s, "/")
	label, _, dotted := strings.Cut(host, ".")
	if !dotted || label == "" || label == "www" {
		return ""
	}
	for _, r := range label {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	return label
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

// idArg reads an optional id (a JSON number or a string of digits); "" when
// it is absent.
func idArg(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	var id string
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) || v <= 0 {
			return "", fmt.Errorf("%s must be an id from an earlier answer", key)
		}
		id = strconv.FormatInt(int64(v), 10)
	case string:
		id = strings.TrimSpace(v)
	default:
		return "", fmt.Errorf("%s must be an id from an earlier answer", key)
	}
	if id == "" {
		return "", nil
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
		return "", fmt.Errorf("%s must be an id from an earlier answer", key)
	}
	return id, nil
}

// itemArgs reads site, collection and a positive whole-number item id.
func itemArgs(args map[string]any) (site, coll, id string, err error) {
	if site, err = siteArg(args); err != nil {
		return
	}
	if coll, err = stringArg(args, "collection"); err != nil {
		return
	}
	var raw any = args["id"]
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) || v <= 0 {
			return "", "", "", errors.New("id must be the item id from read_collection")
		}
		id = strconv.FormatInt(int64(v), 10)
	default:
		if id, err = stringArg(args, "id"); err != nil {
			return
		}
		id = strings.TrimSpace(id)
	}
	if n, perr := strconv.ParseInt(id, 10, 64); perr != nil || n <= 0 {
		return "", "", "", errors.New("id must be the item id from read_collection")
	}
	return site, coll, id, nil
}

const siteDesc = "The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens."

// listArg reads a list argument. A client whose copy of the tool list predates
// the argument (ChatGPT keeps the one from the day it connected) sends the
// list as a JSON string, so that form is decoded too.
func listArg(raw any) ([]any, bool) {
	if text, isText := raw.(string); isText {
		var decoded []any
		if json.Unmarshal([]byte(text), &decoded) != nil {
			return nil, false
		}
		return decoded, true
	}
	list, ok := raw.([]any)
	return list, ok
}
