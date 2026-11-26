package testkit

import (
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Validate is enough JSON Schema for the tools' output schemas: type (or a
// list of types), enum, properties, required, additionalProperties, items.
// It returns every mismatch, in Ajv's terms.
func Validate(schema map[string]any, v any, at string) []string {
	var errs []string
	if t, ok := schema["type"]; ok {
		types := []any{t}
		if list, ok := t.([]any); ok {
			types = list
		}
		match := false
		for _, t := range types {
			match = match || typeIs(t.(string), v)
		}
		if !match {
			return []string{fmt.Sprintf("%s must be %v (is %T)", at, t, v)}
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		in := false
		for _, e := range enum {
			in = in || reflect.DeepEqual(e, v)
		}
		if !in {
			errs = append(errs, fmt.Sprintf("%s must be one of %v (is %v)", at, enum, v))
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		req, _ := schema["required"].([]any)
		for _, r := range req {
			if _, ok := x[r.(string)]; !ok {
				errs = append(errs, fmt.Sprintf("%s must have required property '%s'", at, r))
			}
		}
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if sub, ok := props[key].(map[string]any); ok {
				errs = append(errs, Validate(sub, x[key], at+"/"+key)...)
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					errs = append(errs, fmt.Sprintf("%s must NOT have additional property '%s'", at, key))
				}
			case map[string]any:
				errs = append(errs, Validate(extra, x[key], at+"/"+key)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, el := range x {
				errs = append(errs, Validate(items, el, fmt.Sprintf("%s/%d", at, i))...)
			}
		}
	}
	return errs
}

func typeIs(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}
