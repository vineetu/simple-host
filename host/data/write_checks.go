package data

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StampID identifies the author of a new entry.
func StampID(owner bool, visitorID string) string {
	if owner {
		return ""
	}
	return visitorID
}

// CheckPostLimits keeps a post from someone not signed in small and mostly
// free of links. value is the JSON the page sent.
func CheckPostLimits(value json.RawMessage, maxBytes, maxLinks int) error {
	if len(value) > maxBytes {
		return &Error{Code: "post_too_large", Message: fmt.Sprintf("a post here is at most %d bytes", maxBytes)}
	}
	var v any
	if json.Unmarshal(value, &v) != nil {
		return &Error{Code: "invalid_value", Message: "valid JSON required"}
	}
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			b.WriteString(strings.ToLower(t))
			b.WriteByte(' ')
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for k, e := range t {
				walk(k)
				walk(e)
			}
		}
	}
	walk(v)
	text := b.String()
	if strings.Count(text, "http://")+strings.Count(text, "https://")+strings.Count(text, "www.") > maxLinks {
		return &Error{Code: "too_many_links", Message: fmt.Sprintf("a post here may hold at most %d links", maxLinks)}
	}
	return nil
}
