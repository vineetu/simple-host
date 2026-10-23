package mcp

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// Everything a basic build needs, including every setup call the page recipes name (SIM-187).
var coreToolNames = []string{
	"who_am_i", "list_sites", "get_site", "read_site_file", "create_site", "update_site",
	"get_page_recipe", "create_upload_link", "storage_set_resource", "storage_sql_schema",
	"storage_sql_query", "storage_sql_execute", "storage_get_kv", "storage_put_kv",
	"storage_put_file", "storage_visitor_emails", "set_site_access", "grant_site_viewer",
	"find_tools", "run_tool",
}

var toolAliases = map[string][]string{
	"connect_domain":             {"domain", "own domain", "custom domain", "url", "address", "shorter link", "short name", "dns", "www"},
	"domain_status":              {"check domain dns", "dns status", "certificate", "connected"},
	"remove_domain":              {"disconnect domain", "remove domain", "unlink address"},
	"set_site_passcode":          {"password", "lock", "pin", "protect", "code"},
	"get_site_passcode":          {"show password", "read passcode", "forgot code"},
	"remove_site_passcode":       {"remove passcode", "remove password", "unlock"},
	"sign_out_site_visitors":     {"sign out visitors", "logout", "end sessions"},
	"list_site_viewers":          {"viewers", "who has access", "allowed emails"},
	"revoke_site_viewer":         {"remove viewer", "revoke access", "uninvite"},
	"rollback_site":              {"undo", "revert", "restore previous", "go back", "broke", "previous version", "last change"},
	"list_versions":              {"versions", "history", "revisions"},
	"preview_version":            {"preview", "draft", "older version"},
	"set_keep_versions":          {"keep versions", "retention", "version count"},
	"site_analytics":             {"visitors", "visits", "traffic", "views", "stats", "numbers", "how many people"},
	"list_notifications":         {"webhook", "notify", "alert", "email me", "when someone books"},
	"remove_notification":        {"remove webhook", "stop notifications", "cancel alert", "stop email me"},
	"export_site":                {"download", "backup", "copy", "archive"},
	"delete_site":                {"delete", "remove site", "trash"},
	"restore_site":               {"undelete", "recently deleted", "bring back", "deleted site"},
	"list_deleted_sites":         {"undelete", "recently deleted", "trash list"},
	"set_visibility":             {"unlist", "hide", "list", "search engines", "public page"},
	"set_site_offline":           {"offline", "pause", "maintenance", "take down"},
	"rename_site":                {"rename", "new name", "change name"},
	"set_home_page":              {"home page", "homepage", "profile landing"},
	"set_bio":                    {"bio", "biography", "profile description"},
	"set_showcase_site":          {"pin", "showcase", "profile order"},
	"keep_site":                  {"keep site", "renew", "expiry"},
	"create_import_link":         {"wordpress", "squarespace", "import", "move my site", "migrate"},
	"get_import_report":          {"import report", "migration result", "wordpress progress"},
	"storage_list_resources":     {"saved data", "presets", "who can read", "access", "resources"},
	"storage_get_usage":          {"space", "usage", "quota", "full", "storage left"},
	"storage_list_file_objects":  {"uploads", "uploaded photos", "files", "photos visitors uploaded"},
	"storage_file_download_link": {"download a file", "get photo", "file link"},
	"storage_list_kv_keys":       {"keys", "saved settings", "list values"},
	"storage_delete_resource":    {"delete resource", "drop database", "remove bucket"},
	"storage_delete_kv":          {"delete key", "remove value", "erase setting"},
	"storage_delete_file":        {"delete file", "remove photo", "erase upload"},
}

func discoveryTools() []Tool {
	return []Tool{
		{
			Name: "find_tools", Title: "Find more Simple Host tools",
			Description: "Find tools for a job: own domain or short free address, passcode, rollback, rename, delete and restore, visitor numbers, home page, export, notifications, moving a WordPress site, or more storage. Returns matching tools and arguments; call your pick with run_tool.",
			InputSchema: object(map[string]any{
				"query": str("The job in a few words; empty lists the other tools."),
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "default": 3},
			}, "query"),
			OutputSchema: outObject(map[string]any{
				"tools": outArray("Matching tools, or the catalog when no terms match.", outObject(map[string]any{
					"name": outString("Tool name."), "title": outString("Tool title."),
					"description": outString("What the tool does and when to ask first."),
					"arguments":   map[string]any{"type": "object", "additionalProperties": true},
					"listed":      outBool("Already listed: call it directly."),
				}, "name", "title")),
				"next": outString("How to call the tool."),
			}, "tools", "next"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				query, ok := args["query"].(string)
				if !ok {
					return output{}, fmt.Errorf("query must be a string")
				}
				limit := 3
				if raw, present := args["limit"]; present {
					n, ok := raw.(float64)
					if !ok || n < 1 || n > 10 || n != math.Trunc(n) {
						return output{}, fmt.Errorf("limit must be an integer from 1 to 10")
					}
					limit = int(n)
				}
				return c.server.searchFor(c.caller).find(query, limit), nil
			},
		},
		{
			Name: "run_tool", Title: "Run a Simple Host tool",
			Description: "Run a Simple Host tool by name with its arguments, from find_tools or a skill, recipe, or result naming a tool outside your list. The same checks apply; ask the person first where that tool's description says so.",
			InputSchema: object(map[string]any{
				"name":      str("The tool to call."),
				"arguments": map[string]any{"type": "object", "additionalProperties": true, "description": "The tool's arguments; defaults to {}."},
			}, "name"),
			Annotations: writes(true, false, true),
		},
	}
}

type searchableTool struct {
	tool    Tool
	weights map[string]float64
	phrases []string
	listed  bool
}

type toolSearch struct {
	tools []searchableTool
	idf   map[string]float64
}

func searchTerms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	terms := make([]string, 0, len(words))
	for _, word := range words {
		switch word {
		case "a", "an", "the", "my", "our", "me", "i", "it", "to", "for", "of", "on", "in", "is", "are", "and", "from", "with", "this":
			continue
		}
		if len(word) > 4 && strings.HasSuffix(word, "ies") {
			word = strings.TrimSuffix(word, "ies") + "y"
		} else if len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = strings.TrimSuffix(word, "s")
		}
		switch {
		case len(word) > 5 && strings.HasSuffix(word, "ing"):
			word = strings.TrimSuffix(word, "ing")
		case len(word) > 4 && strings.HasSuffix(word, "ed"):
			word = strings.TrimSuffix(word, "ed")
		}
		if len(word) > 4 {
			word = strings.TrimSuffix(word, "e")
		}
		terms = append(terms, word)
	}
	return terms
}

func newToolSearch(tools []Tool) *toolSearch {
	s := &toolSearch{idf: map[string]float64{}}
	listed := map[string]bool{}
	for _, name := range coreToolNames {
		listed[name] = true
	}
	for _, tool := range tools {
		if tool.Name == "find_tools" || tool.Name == "run_tool" {
			continue
		}
		entry := searchableTool{tool: tool, weights: map[string]float64{}, listed: listed[tool.Name]}
		add := func(text string, weight float64) {
			for _, term := range searchTerms(text) {
				if weight > entry.weights[term] {
					entry.weights[term] = weight
				}
			}
		}
		add(tool.Name, 4)
		add(tool.Title, 2)
		add(tool.Description, 1)
		for _, alias := range toolAliases[tool.Name] {
			add(alias, 3)
			terms := searchTerms(alias)
			if len(terms) > 1 {
				entry.phrases = append(entry.phrases, strings.Join(terms, " "))
			}
		}
		for term := range entry.weights {
			s.idf[term]++
		}
		s.tools = append(s.tools, entry)
	}
	for term, count := range s.idf {
		s.idf[term] = math.Log(1 + float64(len(s.tools))/count)
	}
	return s
}

func (s *toolSearch) rank(query string, limit int) []searchableTool {
	terms := searchTerms(query)
	phrase := " " + strings.Join(terms, " ") + " "
	type match struct {
		entry searchableTool
		score float64
	}
	matches := []match{}
	for _, entry := range s.tools {
		score := 0.0
		for _, term := range terms {
			score += s.idf[term] * entry.weights[term]
		}
		for _, alias := range entry.phrases {
			if strings.Contains(phrase, " "+alias+" ") {
				score += 12
			}
		}
		if strings.Contains(strings.ToLower(query), entry.tool.Name) {
			score += 1000
		}
		if score > 0 {
			matches = append(matches, match{entry, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]searchableTool, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.entry)
	}
	return result
}

func (s *toolSearch) find(query string, limit int) output {
	matches := s.rank(query, limit)
	catalog := strings.TrimSpace(query) == "" || len(matches) == 0
	lines := []string{}
	if catalog {
		matches = nil
		for _, entry := range s.tools {
			if !entry.listed {
				matches = append(matches, entry)
			}
		}
		if strings.TrimSpace(query) != "" {
			lines = append(lines, "Nothing matched. Describe the job differently or pick from this list.")
		}
	}
	items := make([]any, 0, len(matches))
	for _, entry := range matches {
		tool := entry.tool
		item := map[string]any{"name": tool.Name, "title": tool.Title}
		how := "run_tool"
		if entry.listed {
			how = "call it directly"
		}
		if !catalog {
			item["description"], item["arguments"], item["listed"] = tool.Description, tool.InputSchema, entry.listed
			properties, _ := tool.InputSchema["properties"].(map[string]any)
			required, _ := tool.InputSchema["required"].([]string)
			names := make([]string, 0, len(properties))
			for name := range properties {
				names = append(names, name)
			}
			sort.Slice(names, func(i, j int) bool {
				ri, rj := slices.Contains(required, names[i]), slices.Contains(required, names[j])
				if ri != rj {
					return ri
				}
				return names[i] < names[j]
			})
			arguments := make([]string, 0, len(names))
			for _, name := range names {
				property, _ := properties[name].(map[string]any)
				kind := "object"
				switch value := property["type"].(type) {
				case string:
					kind = value
				case []string:
					kind = strings.Join(value, "|")
				case []any:
					types := make([]string, 0, len(value))
					for _, typ := range value {
						types = append(types, typ.(string))
					}
					kind = strings.Join(types, "|")
				}
				if slices.Contains(required, name) {
					kind += ", required"
				}
				argument := fmt.Sprintf("%s (%s)", name, kind)
				if description, _ := property["description"].(string); description != "" {
					argument += ": " + description
				}
				arguments = append(arguments, argument)
			}
			lines = append(lines, fmt.Sprintf("%s (%s): %s\nArguments: %s", tool.Name, how, tool.Description, strings.Join(arguments, "; ")))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %s (%s)", tool.Name, tool.Title, how))
		}
		items = append(items, item)
	}
	return output{Text: strings.Join(lines, "\n"), Structured: map[string]any{
		"tools": items, "next": `Call one with run_tool {"name": ..., "arguments": {...}}`,
	}}
}

func (s *Server) searchFor(caller Caller) *toolSearch {
	if caller.DeployOnly {
		return s.deploySearch
	}
	return s.search
}
