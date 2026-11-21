package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// A deliberately small JSON Schema checker, for tests: it understands exactly
// the keywords the output schemas use (type, including type arrays for
// nullable values; properties; required; items; additionalProperties; enum;
// description) and refuses any other keyword, so a schema cannot quietly rely
// on one that goes unchecked. It lives outside a _test file because both this
// package's tests and host/handler's end-to-end tests use it; nothing in
// the server calls it, so the linker leaves it out of the binary.

var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "items": true,
	"additionalProperties": true, "enum": true, "description": true,
}

var schemaTypes = map[string]bool{
	"object": true, "array": true, "string": true, "integer": true,
	"number": true, "boolean": true, "null": true,
}

// normalizeJSON round-trips v through JSON, so Go-built schemas ([]string,
// map[string]any) and wire-decoded ones ([]any) look the same.
func normalizeJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal(b, &out)
	return out, err
}

// CheckOutputSchema reports whether schema is a well-formed JSON Schema object
// of the kind MCP requires for Tool.outputSchema: a root of type "object",
// using only the supported keywords, each with a value of the right shape.
func CheckOutputSchema(schema any) error {
	norm, err := normalizeJSON(schema)
	if err != nil {
		return err
	}
	root, ok := norm.(map[string]any)
	if !ok {
		return fmt.Errorf("schema is %T, not an object", norm)
	}
	if root["type"] != "object" {
		return fmt.Errorf("root type is %v, want \"object\"", root["type"])
	}
	return checkSchemaNode(root, "$")
}

func checkSchemaNode(node map[string]any, at string) error {
	if err := checkSchemaKeywords(node, at); err != nil {
		return err
	}
	types, err := schemaTypeList(node, at)
	if err != nil {
		return err
	}
	for _, step := range []func(map[string]any, []string, string) error{
		checkSchemaProperties, checkSchemaRequired, checkSchemaAdditional, checkSchemaItems, checkSchemaEnum,
	} {
		if err := step(node, types, at); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaKeywords(node map[string]any, at string) error {
	for _, k := range sortedKeys(node) {
		if !schemaKeywords[k] {
			return fmt.Errorf("%s: unsupported keyword %q", at, k)
		}
	}
	if d, present := node["description"]; present {
		if _, ok := d.(string); !ok {
			return fmt.Errorf("%s: description must be a string", at)
		}
	}
	return nil
}

// allowsType: types (empty means any) admits t.
func allowsType(types []string, t string) bool {
	return len(types) == 0 || slices.Contains(types, t)
}

func checkSchemaProperties(node map[string]any, types []string, at string) error {
	raw, present := node["properties"]
	if !present {
		return nil
	}
	if !allowsType(types, "object") {
		return fmt.Errorf("%s: properties on a non-object type", at)
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: properties must be an object", at)
	}
	for _, name := range sortedKeys(props) {
		sub, ok := props[name].(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s: property schema must be an object", at, name)
		}
		if err := checkSchemaNode(sub, at+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaRequired(node map[string]any, _ []string, at string) error {
	raw, present := node["required"]
	if !present {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s: required must be an array", at)
	}
	props, _ := node["properties"].(map[string]any)
	seen := map[string]bool{}
	for _, r := range list {
		name, ok := r.(string)
		if !ok {
			return fmt.Errorf("%s: required entries must be strings", at)
		}
		if seen[name] {
			return fmt.Errorf("%s: %q required twice", at, name)
		}
		seen[name] = true
		if _, declared := props[name]; !declared {
			return fmt.Errorf("%s: %q is required but not a declared property", at, name)
		}
	}
	return nil
}

func checkSchemaAdditional(node map[string]any, _ []string, at string) error {
	raw, present := node["additionalProperties"]
	if !present {
		return nil
	}
	switch v := raw.(type) {
	case bool:
		return nil
	case map[string]any:
		return checkSchemaNode(v, at+"[*]")
	default:
		return fmt.Errorf("%s: additionalProperties must be a boolean or a schema", at)
	}
}

func checkSchemaItems(node map[string]any, types []string, at string) error {
	raw, present := node["items"]
	if !present {
		if len(types) == 1 && types[0] == "array" {
			return fmt.Errorf("%s: an array needs an items schema", at)
		}
		return nil
	}
	if !allowsType(types, "array") {
		return fmt.Errorf("%s: items on a non-array type", at)
	}
	sub, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: items must be a schema", at)
	}
	return checkSchemaNode(sub, at+"[]")
}

func checkSchemaEnum(node map[string]any, _ []string, at string) error {
	raw, present := node["enum"]
	if !present {
		return nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return fmt.Errorf("%s: enum must be a non-empty array", at)
	}
	typeOnly := map[string]any{}
	if t, present := node["type"]; present {
		typeOnly["type"] = t
	}
	for _, v := range list {
		if err := validateNode(typeOnly, v, at+"(enum)", nil); err != nil {
			return fmt.Errorf("%s: enum value %v does not match the type", at, v)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// schemaTypeList reads "type" as a list; empty means any type.
func schemaTypeList(node map[string]any, at string) ([]string, error) {
	raw, present := node["type"]
	if !present {
		return nil, nil
	}
	var list []any
	switch v := raw.(type) {
	case string:
		list = []any{v}
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("%s: type array is empty", at)
		}
		list = v
	default:
		return nil, fmt.Errorf("%s: type must be a string or an array of strings", at)
	}
	out := make([]string, 0, len(list))
	for _, t := range list {
		name, ok := t.(string)
		if !ok || !schemaTypes[name] {
			return nil, fmt.Errorf("%s: unknown type %v", at, t)
		}
		out = append(out, name)
	}
	return out, nil
}

// ValidateOutput checks value (any JSON-shaped Go value) against schema. When
// seen is not nil, it records the path of every declared property that was
// present in value, so a caller can confirm a schema declares nothing a tool
// never returns.
func ValidateOutput(schema, value any, seen map[string]bool) error {
	s, err := normalizeJSON(schema)
	if err != nil {
		return err
	}
	v, err := normalizeJSON(value)
	if err != nil {
		return err
	}
	node, ok := s.(map[string]any)
	if !ok {
		return fmt.Errorf("schema is %T, not an object", s)
	}
	return validateNode(node, v, "$", seen)
}

func validateNode(node map[string]any, value any, at string, seen map[string]bool) error {
	if err := validateType(node, value, at); err != nil {
		return err
	}
	if raw, present := node["enum"]; present && !slices.ContainsFunc(raw.([]any), func(allowed any) bool { return reflect.DeepEqual(allowed, value) }) {
		return fmt.Errorf("%s: %s is not one of %v", at, describe(value), raw)
	}
	switch v := value.(type) {
	case map[string]any:
		return validateObject(node, v, at, seen)
	case []any:
		items, ok := node["items"].(map[string]any)
		if !ok {
			return nil
		}
		for i, el := range v {
			// Items share one path, so coverage is per shape, not per index.
			if err := validateNode(items, el, at+"[]", seen); err != nil {
				return fmt.Errorf("%w (item %d)", err, i)
			}
		}
	}
	return nil
}

func validateType(node map[string]any, value any, at string) error {
	types, err := schemaTypeList(node, at)
	if err != nil || len(types) == 0 {
		return err
	}
	if slices.ContainsFunc(types, func(t string) bool { return jsonTypeMatches(t, value) }) {
		return nil
	}
	return fmt.Errorf("%s: %s is not of type %s", at, describe(value), strings.Join(types, "|"))
}

func validateObject(node map[string]any, v map[string]any, at string, seen map[string]bool) error {
	if req, ok := node["required"].([]any); ok {
		for _, r := range req {
			if _, present := v[r.(string)]; !present {
				return fmt.Errorf("%s: required property %q is missing", at, r)
			}
		}
	}
	props, _ := node["properties"].(map[string]any)
	for _, k := range sortedKeys(v) {
		sub, declared := props[k].(map[string]any)
		if declared && seen != nil {
			seen[at+"."+k] = true
		}
		if !declared {
			switch extra := node["additionalProperties"].(type) {
			case bool:
				if !extra {
					return fmt.Errorf("%s: property %q is not allowed", at, k)
				}
				continue
			case map[string]any:
				sub = extra
			default:
				continue
			}
		}
		if err := validateNode(sub, v[k], at+"."+k, seen); err != nil {
			return err
		}
	}
	return nil
}

func jsonTypeMatches(t string, value any) bool {
	switch t {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	}
	return false
}

func describe(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// SchemaPropertyPaths lists the path of every property a schema declares, in
// the form ValidateOutput records them.
func SchemaPropertyPaths(schema any) []string {
	norm, _ := normalizeJSON(schema)
	var out []string
	var walk func(node map[string]any, at string)
	walk = func(node map[string]any, at string) {
		if props, ok := node["properties"].(map[string]any); ok {
			for name, sub := range props {
				out = append(out, at+"."+name)
				if m, ok := sub.(map[string]any); ok {
					walk(m, at+"."+name)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, at+"[]")
		}
	}
	if root, ok := norm.(map[string]any); ok {
		walk(root, "$")
	}
	sort.Strings(out)
	return out
}
