package mcp

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const analyticsFieldBytes = 8192

// The only pattern is the SDK's binary-data test, not a personal-data redactor.
var analyticsBase64 = regexp.MustCompile(`^[A-Za-z0-9+/\n\r]+=*$`)

func analyticsParameters(tool string, args map[string]any) any {
	return capAnalyticsJSON(sdkSanitizeParameters(map[string]any{
		"arguments": dropAnalyticsValues(tool, analyticsCopy(args)),
	}))
}

func analyticsCopy(value any) any {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var copy any
	_ = json.Unmarshal(body, &copy)
	return copy
}

func analyticsResponse(tool string, value any) any {
	original, _ := value.(map[string]any)
	// Never include result metadata, which can hold UI-only credentials.
	response := map[string]any{"content": analyticsCopy(original["content"]), "isError": original["isError"]}
	if structured, exists := original["structuredContent"]; exists {
		response["structuredContent"] = dropAnalyticsValues(tool, analyticsCopy(structured))
	}
	blocks, _ := response["content"].([]any)
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] != "text" {
			dropAnalyticsValues(tool, block)
			continue
		}
		text, _ := block["text"].(string)
		switch {
		case analyticsSecretText(tool):
			block["text"] = "[removed: contains a secret]"
		case analyticsFileResult(tool):
			// File-read prose includes headers as well as the bytes. The
			// structured copy has each file's real size and path/type.
			if structured := response["structuredContent"]; structured != nil {
				block["text"] = jsonText(structured)
			} else {
				block["text"] = jsonText(analyticsFile(text, "", false))
			}
		case tool == "who_am_i" || strings.Contains(tool, "site_viewer") || tool == "set_site_access" || tool == "storage_visitor_emails":
			block["text"] = jsonText(response["structuredContent"])
		default:
			// Storage and Hack tools also return their JSON as text. Walk
			// that representation so removing a structured field is enough.
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				block["text"] = jsonText(sdkSanitizeParameters(dropAnalyticsValues(tool, decoded)))
			} else if data, ok := analyticsDataURI(text); ok {
				block["text"] = jsonText(data)
			}
		}
	}
	return capAnalyticsJSON(sdkSanitizeResponse(response))
}

func analyticsFieldName(name string) string {
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
}

func analyticsSecretField(tool, name string) bool {
	name = analyticsFieldName(name)
	for _, part := range []string{"passcode", "password", "secret", "token", "apikey", "authorization", "cookie"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	switch name {
	case "code", "signincode", "verificationcode", "teamkey", "signingkey", "privatekey", "authheader",
		"uploadurl", "photosurl", "importurl", "downloadurl", "signedurl", "previewurl", "pinnedurl",
		"joincode", "judgecode", "joinurl", "judgeurl", "inviteurl":
		return true
	case "key":
		return tool == "hack_create_team_key" || tool == "hack_get_team_key"
	case "how":
		return tool == "create_upload_link" // embeds the signed URL in a curl command
	case "url":
		switch tool {
		case "preview_version", "export_site", "storage_file_download_link", "hack_event_storage_file_download_link",
			"hack_export_projects_archive", "hack_export_own_team_archive", "hack_create_organiser_invite", "hack_regenerate_code":
			return true
		}
	}
	return false
}

func analyticsSecretText(tool string) bool {
	switch tool {
	case "get_site_passcode", "set_site_passcode", "remove_site_passcode", "sign_out_site_visitors",
		"create_upload_link", "create_import_link", "preview_version", "export_site", "update_site",
		"storage_file_download_link", "hack_event_storage_file_download_link",
		"hack_create_team_key", "hack_get_team_key", "hack_export_projects_archive", "hack_export_own_team_archive",
		"hack_create_event", "hack_get_event", "hack_update_event", "hack_set_event_stage", "hack_regenerate_code",
		"hack_create_organiser_invite":
		return true
	}
	return false
}

func analyticsEmailField(tool, name string) bool {
	name = analyticsFieldName(name)
	if (tool == "grant_site_viewer" || tool == "revoke_site_viewer" || tool == "set_site_access" || tool == "list_site_viewers") &&
		(name == "added" || name == "alreadylisted" || name == "removed") {
		return true
	}
	if name == "visitoremail" || name == "visitoremails" || (strings.HasPrefix(tool, "hack_") && name == "contactemail") {
		return true
	}
	if name != "email" && name != "emails" {
		return false
	}
	return tool == "who_am_i" || strings.Contains(tool, "storage_") ||
		strings.Contains(tool, "site_viewer") || tool == "set_site_access" || strings.HasPrefix(tool, "hack_")
}

func analyticsVisitorReference(obj map[string]any) string {
	for _, name := range []string{"visitor_id", "visitor_ref", "visitor"} {
		if ref, ok := obj[name].(string); ok && strings.HasPrefix(ref, "v_") {
			return ref
		}
	}
	return "[removed]"
}

func analyticsFileResult(tool string) bool {
	return tool == "read_site_file" || tool == "hack_export_participants" ||
		tool == "hack_export_teams" || tool == "hack_export_entries" ||
		tool == "hack_export_scores" || tool == "hack_export_results"
}

func analyticsFile(value any, kind string, encoded bool) any {
	text, ok := value.(string)
	if !ok {
		body, _ := json.Marshal(value)
		text = string(body)
	}
	if data, ok := analyticsDataURI(text); ok {
		return data
	}
	n := len(text)
	if encoded {
		n = analyticsBase64Bytes(text)
	}
	if kind == "" {
		kind = "unknown"
	}
	return map[string]any{"removed": "file", "bytes": n, "type": kind}
}

func analyticsBase64Bytes(text string) int {
	n, padding := 0, 0
	for _, char := range text {
		if char == '=' {
			padding++
		}
		if char != '\r' && char != '\n' {
			n++
		}
	}
	return max(0, n*3/4-padding)
}

func analyticsDataURI(text string) (any, bool) {
	if !strings.HasPrefix(text, "data:") {
		return nil, false
	}
	header, body, ok := strings.Cut(text[5:], ";base64,")
	if !ok {
		return nil, false
	}
	kind, _, _ := strings.Cut(header, ";")
	if kind == "" {
		kind = "text/plain"
	}
	return map[string]any{"removed": "file", "bytes": analyticsBase64Bytes(body), "type": kind}, true
}

func analyticsFileType(obj map[string]any) string {
	for _, key := range []string{"content_type", "mimeType", "mime_type"} {
		if value, ok := obj[key].(string); ok && value != "" {
			return value
		}
	}
	for _, key := range []string{"path", "filename"} {
		if name, ok := obj[key].(string); ok {
			return path.Ext(name)
		}
	}
	return ""
}

// Work on a JSON copy. Tool-specific field names cover binary files, signed
// links, and visitor identity; ordinary conversation is left to AgentCat's
// server-side redaction. No content-pattern matching is done here.
func dropAnalyticsValues(tool string, value any) any {
	return dropAnalyticsValue(tool, value, 0)
}

func dropAnalyticsValue(tool string, value any, depth int) any {
	if depth >= 32 {
		return "[Object]"
	}
	switch v := value.(type) {
	case string:
		if data, ok := analyticsDataURI(v); ok {
			return data
		}
	case []any:
		for i := range v {
			v[i] = dropAnalyticsValue(tool, v[i], depth+1)
		}
	case map[string]any:
		// SQLite represents objects as column names plus positional rows.
		columns, hasColumns := v["columns"].([]any)
		rows, hasRows := v["rows"].([]any)
		handledRows := hasColumns && hasRows
		if handledRows {
			if depth+1 >= 32 {
				v["rows"] = "[Object]"
			} else {
				for r, raw := range rows {
					cells, ok := raw.([]any)
					if !ok || depth+2 >= 32 {
						rows[r] = dropAnalyticsValue(tool, raw, depth+2)
						continue
					}
					obj := map[string]any{}
					for i, col := range columns {
						if name, ok := col.(string); ok && i < len(cells) {
							obj[name] = cells[i]
						}
					}
					for i, cell := range cells {
						name := ""
						if i < len(columns) {
							name, _ = columns[i].(string)
						}
						cells[i] = dropAnalyticsField(tool, name, cell, obj, depth+3)
					}
				}
			}
		}
		for key, field := range v {
			if key == "rows" && handledRows {
				continue
			}
			v[key] = dropAnalyticsField(tool, key, field, v, depth+1)
		}
	}
	return value
}

func dropAnalyticsField(tool, key string, field any, obj map[string]any, depth int) any {
	if depth >= 32 {
		return "[Object]"
	}
	name := analyticsFieldName(key)
	switch {
	case tool == "visitor_read_site" && name == "pagetext":
		return analyticsFile(field, "text/plain", false)
	case analyticsSecretField(tool, key):
		return "[removed]"
	case analyticsEmailField(tool, key):
		return analyticsVisitorReference(obj)
	case (name == "files" && (tool == "create_site" || tool == "update_site" || tool == "hack_publish_event_website")) || name == "filesbase64":
		if text, ok := field.(string); ok {
			var parsed any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				field = parsed
			}
		}
		if files, ok := field.(map[string]any); ok {
			for file, content := range files {
				files[file] = analyticsFile(content, path.Ext(file), name == "filesbase64")
			}
			return files
		}
		return analyticsFile(field, "", name == "filesbase64")
	case name == "edits" && tool == "update_site":
		return analyticsEdits(field, depth)
	case strings.HasSuffix(strings.ToLower(key), "_base64") || name == "base64":
		kind := analyticsFileType(obj)
		if name == "archivebase64" {
			kind = "application/gzip"
		}
		return analyticsFile(field, kind, true)
	case (tool == "update_site" && (name == "find" || name == "replace")) ||
		(analyticsFileResult(tool) && (name == "content" || name == "csv")) ||
		(tool == "storage_put_file" && name == "data"):
		file := analyticsFile(field, analyticsFileType(obj), false)
		if tool == "read_site_file" && name == "content" {
			if size, ok := obj["size"].(float64); ok {
				file.(map[string]any)["bytes"] = size
			}
		}
		return file
	default:
		return dropAnalyticsValue(tool, field, depth)
	}
}

func analyticsEdits(value any, depth int) any {
	if text, ok := value.(string); ok {
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			value = parsed
		}
	}
	edits, ok := value.([]any)
	if !ok {
		return analyticsFile(value, "", false)
	}
	for i, raw := range edits {
		if depth+1 >= 32 {
			edits[i] = "[Object]"
			continue
		}
		edit, ok := raw.(map[string]any)
		_, hasPath := edit["path"]
		_, hasFind := edit["find"]
		_, hasReplace := edit["replace"]
		if !ok || !(hasPath || hasFind || hasReplace) {
			edits[i] = analyticsFile(raw, "", false)
			continue
		}
		filename, _ := edit["path"].(string)
		kind := path.Ext(filename)
		for key, field := range edit {
			if text, ok := field.(string); key == "path" && ok {
				edit[key] = dropAnalyticsValue("update_site", text, depth+2)
			} else {
				edit[key] = analyticsFile(field, kind, false)
			}
		}
	}
	return edits
}

// Port of mcpcat-ts sanitization.ts: same gate, pattern, and placeholders.
func sdkSanitizeParameters(value any) any {
	switch v := value.(type) {
	case string:
		if len(v) >= 10240 && analyticsBase64.MatchString(v) {
			return "[binary data redacted - not supported by AgentCat]"
		}
	case []any:
		for i := range v {
			v[i] = sdkSanitizeParameters(v[i])
		}
	case map[string]any:
		for key := range v {
			v[key] = sdkSanitizeParameters(v[key])
		}
	}
	return value
}

func sdkSanitizeResponse(response map[string]any) any {
	blocks, _ := response["content"].([]any)
	for i, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var placeholder string
		switch block["type"] {
		case "text", "resource_link":
			continue
		case "image", "audio":
			placeholder = fmt.Sprintf("[%s content redacted - not supported by AgentCat]", block["type"])
		case "resource":
			resource, _ := block["resource"].(map[string]any)
			if _, blob := resource["blob"]; !blob {
				continue
			}
			placeholder = "[binary resource content redacted - not supported by AgentCat]"
		default:
			kind, exists := block["type"]
			if !exists {
				kind = "undefined"
			} else if kind == nil {
				kind = "null"
			}
			placeholder = fmt.Sprintf("[unsupported content type \"%v\" redacted - not supported by AgentCat]", kind)
		}
		blocks[i] = map[string]any{"type": "text", "text": placeholder}
	}
	if structured, exists := response["structuredContent"]; exists {
		response["structuredContent"] = sdkSanitizeParameters(structured)
	}
	return response
}

func truncateAnalyticsString(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "..."
}

func capAnalyticsIntent(text string) string {
	if data, ok := analyticsDataURI(text); ok {
		file := data.(map[string]any)
		text = fmt.Sprintf("[file: %v bytes, %s]", file["bytes"], file["type"])
	}
	text = sdkSanitizeParameters(text).(string)
	if utf8.RuneCountInString(text) > 2048 {
		return truncateAnalyticsString(text, 2045)
	}
	return text
}

// SDK normalization, with JSON-only inputs and a strict per-field byte cap.
// Sorted keys make breadth limiting reproducible in Go. Depth is reduced first,
// then string/breadth limits, including unusually large object keys.
func normalizeAnalytics(value any, depth, breadth, stringLimit int) any {
	switch v := value.(type) {
	case string:
		return truncateAnalyticsString(v, stringLimit)
	case []any:
		if depth <= 0 {
			return "[Array]"
		}
		out := make([]any, 0, min(len(v), breadth)+1)
		for i, item := range v {
			if i == breadth {
				out = append(out, "[MaxProperties ~]")
				break
			}
			out = append(out, normalizeAnalytics(item, depth-1, breadth, stringLimit))
		}
		return out
	case map[string]any:
		if depth <= 0 {
			return "[Object]"
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for i, key := range keys {
			if i == breadth {
				out["..."] = "[MaxProperties ~]"
				break
			}
			out[truncateAnalyticsString(key, stringLimit)] = normalizeAnalytics(v[key], depth-1, breadth, stringLimit)
		}
		return out
	}
	return value
}

func capAnalyticsJSON(value any) any {
	var reduced any
	for depth := 10; depth >= 1; depth-- {
		reduced = normalizeAnalytics(value, depth, 100, 8192)
		if body, err := json.Marshal(reduced); err == nil && len(body) <= analyticsFieldBytes {
			return reduced
		}
	}
	for limit := 4096; limit >= 1; limit /= 2 {
		reduced = normalizeAnalytics(value, 1, min(100, limit), limit)
		if body, err := json.Marshal(reduced); err == nil && len(body) <= analyticsFieldBytes {
			return reduced
		}
	}
	return "[Object]"
}

// Field removal also removes those supplied values when repeated in prose.
func analyticsSecretValues(tool string, value any) []string {
	var values []string
	var collect func(any, bool)
	collect = func(v any, secret bool) {
		switch x := v.(type) {
		case string:
			if secret && x != "" {
				values = append(values, x)
				if trimmed := strings.TrimSpace(x); trimmed != "" && trimmed != x {
					values = append(values, trimmed)
				}
			}
		case json.Number:
			if secret {
				values = append(values, x.String())
			}
		case float64:
			// Plain digits, never 1.2345678e+07, so the intent's copy matches.
			if secret {
				values = append(values, strconv.FormatFloat(x, 'f', -1, 64))
			}
		case map[string]any:
			for k, child := range x {
				collect(child, secret || analyticsSecretField(tool, k))
			}
		case []any:
			for _, child := range x {
				collect(child, secret)
			}
		}
	}
	if tool == "run_tool" {
		if args, ok := value.(map[string]any); ok {
			if name, ok := args["name"].(string); ok {
				tool = name
			}
			// Arguments sent as a JSON string are refused, but the event is
			// still recorded; their secrets come out of the intent too.
			if raw, ok := args["arguments"].(string); ok {
				var inner any
				if json.Unmarshal([]byte(raw), &inner) == nil {
					collect(inner, false)
				}
			}
		}
	}
	collect(value, false)
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}
func redactAnalyticsSecrets(value any, secrets []string) any {
	switch x := value.(type) {
	case string:
		for _, secret := range secrets {
			x = strings.ReplaceAll(x, secret, "[removed]")
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, child := range x {
			out[redactAnalyticsSecrets(key, secrets).(string)] = redactAnalyticsSecrets(child, secrets)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = redactAnalyticsSecrets(child, secrets)
		}
		return out
	default:
		return value
	}
}
