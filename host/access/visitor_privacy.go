package access

// What a visitor tool may hand an AI visitor: never an email address or an
// account id, unless it is the structure of the data (VisitorStructure).

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// VisitorEmailPattern and VisitorAccountPattern find an email address and an
// account id in text a visitor tool hands back.
var VisitorEmailPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+`)

var VisitorAccountPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// Only server-selected structural fields use this wrapper, never authored data.
// It preserves addressing and schema metadata through the content sanitizer.
type VisitorStructure struct{ Value any }

// VisitorIdentityField reports whether a field name holds who someone is.
func VisitorIdentityField(name string) bool {
	switch strings.ToLower(name) {
	case "visitor_id", "writer_id", "submitted_by", "_submitted_by", "submitted_email", "actor_id", "actor_email", "email", "emails", "account_id", "user_id", "by":
		return true
	}
	return false
}

// Defence at the result boundary also covers email/account IDs deliberately
// copied into visitor content. Authentication metadata never reaches the tool.
func VisitorPrivateData(v any) any {
	return visitorSanitizeData(v, false)
}

func visitorSanitizeData(v any, structural bool) any {
	switch x := v.(type) {
	case VisitorStructure:
		return visitorSanitizeData(x.Value, true)
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = visitorSanitizeData(e, structural)
		}
		return out
	case string:
		x = VisitorEmailPattern.ReplaceAllString(x, "[email omitted]")
		if structural {
			return x
		}
		return VisitorAccountPattern.ReplaceAllString(x, "[account omitted]")
	case []any:
		for i, e := range x {
			x[i] = visitorSanitizeData(e, structural)
		}
		return x
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if !structural && VisitorIdentityField(k) {
				continue
			}
			out[visitorSanitizeData(k, structural).(string)] = visitorSanitizeData(e, structural)
		}
		return out
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var generic any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&generic) != nil {
			return nil
		}
		switch generic.(type) {
		case map[string]any, []any:
			return visitorSanitizeData(generic, structural)
		}
		return v
	}
}
