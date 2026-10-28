package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// A basic build and the jobs people ask for most (SIM-187). 2026-10-27
// (SIM-226): AgentCat showed storage_list_resources, site_analytics and
// list_versions reached through run_tool more often than storage_get_kv,
// storage_put_file and storage_visitor_emails were called at all, so they
// swapped places.
var coreToolNames = []string{
	"who_am_i", "list_sites", "get_site", "read_site_file", "create_site", "update_site",
	"get_page_recipe", "create_upload_link", "storage_set_resource", "storage_sql_schema",
	"storage_sql_query", "storage_sql_execute", "storage_put_kv", "storage_list_resources",
	"site_analytics", "list_versions", "set_site_access", "grant_site_viewer",
	"find_tools", "run_tool",
}

var toolAliases = map[string][]string{
	"create_upload_link":         {"photo picker", "picker", "add photos", "upload photos", "upload pictures from my phone", "put my photos on the site", "menu photos", "listing photos", "my pictures"},
	"connect_domain":             {"domain", "own domain", "custom domain", "url", "address", "shorter link", "short name", "dns", "www", "change address", "website address"},
	"domain_status":              {"check domain dns", "dns status", "certificate", "connected"},
	"remove_domain":              {"disconnect domain", "remove domain", "unlink address", "disconnect custom domain"},
	"set_site_passcode":          {"password", "lock", "pin", "protect", "code", "make private", "private password"},
	"get_site_passcode":          {"show password", "read passcode", "forgot code"},
	"remove_site_passcode":       {"remove passcode", "remove password", "unlock", "let everyone in", "without code"},
	"sign_out_site_visitors":     {"sign out visitors", "logout", "end sessions"},
	"list_site_viewers":          {"viewers", "who has access", "allowed emails"},
	"revoke_site_viewer":         {"remove viewer", "revoke access", "uninvite"},
	"rollback_site":              {"undo", "revert", "restore previous", "go back", "broke", "previous version", "last change", "put back", "yesterday"},
	"list_versions":              {"versions", "history", "revisions"},
	"preview_version":            {"preview", "draft", "older version", "see old version", "without publishing"},
	"set_keep_versions":          {"keep versions", "retention", "version count"},
	"site_analytics":             {"visitors", "visits", "traffic", "views", "stats", "numbers", "how many people", "who visited"},
	"list_notifications":         {"webhook", "notify", "alert", "email me", "when someone books", "list webhooks"},
	"remove_notification":        {"remove webhook", "stop notifications", "cancel alert", "stop email me"},
	"export_site":                {"download", "backup", "copy", "archive", "entire website", "download website"},
	"delete_site":                {"delete", "remove site", "trash"},
	"restore_site":               {"undelete", "recently deleted", "bring back", "deleted site", "recover", "recover website"},
	"list_deleted_sites":         {"undelete", "recently deleted", "trash list", "show trash"},
	"set_visibility":             {"unlist", "hide", "list", "search engines", "public page"},
	"set_site_offline":           {"offline", "pause", "maintenance", "take down", "temporarily close", "close website", "down for maintenance"},
	"rename_site":                {"rename", "new name", "change name"},
	"set_home_page":              {"home page", "homepage", "profile landing"},
	"set_bio":                    {"bio", "biography", "profile description"},
	"set_showcase_site":          {"pin", "showcase", "profile order"},
	"keep_site":                  {"keep site", "renew", "expiry", "stop expiring", "never expire", "permanent"},
	"create_import_link":         {"wordpress", "squarespace", "import", "move my site", "migrate"},
	"get_import_report":          {"import report", "migration result", "wordpress progress", "import finish", "import done", "import status"},
	"storage_list_resources":     {"saved data", "presets", "who can read", "access", "resources", "database permissions", "visitors read"},
	"storage_get_usage":          {"space", "usage", "quota", "full", "storage left"},
	"storage_list_file_objects":  {"uploads", "uploaded photos", "files", "photos visitors uploaded", "pictures", "images", "show uploads"},
	"storage_file_download_link": {"download a file", "get photo", "file link"},
	"storage_list_kv_keys":       {"keys", "saved settings", "list values"},
	"storage_get_kv":             {"read saved value", "get value", "current menu value", "read setting"},
	"storage_put_file":           {"upload a document", "add a pdf", "store a file", "client document"},
	"storage_visitor_emails":     {"who booked", "who sent", "customer email", "sign-in email", "who placed the order"},
	"storage_delete_resource":    {"delete resource", "drop database", "remove bucket"},
	"storage_delete_kv":          {"delete key", "remove value", "erase setting", "remove saved setting"},
	"storage_delete_file":        {"delete file", "remove photo", "erase upload"},
}

// Operations get a fixed bonus, independent of how rare their words are. Match
// unstemmed query words so "deleted" describes an object, not a delete request.
var actionAliases = map[string][]string{
	"delete":   {"delete", "remove", "erase", "trash"},
	"remove":   {"delete", "remove", "erase", "trash", "disconnect", "unlink"},
	"restore":  {"restore", "recover", "undelete", "bring back"},
	"list":     {"list", "show", "see"},
	"get":      {"get", "show"},
	"set":      {"set"},
	"connect":  {"connect", "link", "attach"},
	"revoke":   {"revoke", "remove", "uninvite"},
	"rollback": {"rollback", "undo", "revert"},
	"export":   {"export", "download", "backup"},
	"rename":   {"rename"},
	"keep":     {"keep", "renew"},
	"read":     {"read"},
	"create":   {"create"},
	"update":   {"update"},
	"preview":  {"preview"},
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
	actions []string
	listed  bool
}

type toolSearch struct {
	flat  bool
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
		if word == "expiry" {
			word = "expire"
		}
		switch word {
		case "website":
			word = "site"
		case "photo", "picture", "image":
			word = "file"
		}
		inflected := false
		switch {
		case len(word) > 4 && strings.HasSuffix(word, "ied"):
			word = strings.TrimSuffix(word, "ied") + "y"
		case len(word) > 5 && strings.HasSuffix(word, "ing"):
			word = strings.TrimSuffix(word, "ing")
			inflected = true
		case len(word) > 4 && strings.HasSuffix(word, "ed"):
			word = strings.TrimSuffix(word, "ed")
			inflected = true
		}
		// Undo consonant doubling in forms such as pinned and stopping.
		if n := len(word); inflected && n > 2 && word[n-1] == word[n-2] && strings.ContainsRune("bdgmnprt", rune(word[n-1])) {
			word = word[:n-1]
		}
		if len(word) > 3 {
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
		add(tool.Description, 0.5)
		for _, alias := range toolAliases[tool.Name] {
			add(alias, 3)
			terms := searchTerms(alias)
			if len(terms) > 1 {
				entry.phrases = append(entry.phrases, strings.Join(terms, " "))
			}
		}
		// Skip namespace/noun components; set_keep_versions also names "keep".
		for _, word := range strings.Split(tool.Name, "_") {
			entry.actions = append(entry.actions, actionAliases[word]...)
		}
		// Read-only tools can also answer "show"/"see" even when their names
		// are nouns, such as site_analytics and domain_status.
		if tool.Annotations["readOnlyHint"] == true {
			entry.actions = append(entry.actions, "show", "see")
		}
		for _, action := range entry.actions {
			add(action, 4)
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
	actionPhrase := " " + strings.Join(strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r < 'a' || r > 'z'
	}), " ") + " "
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
		for _, action := range entry.actions {
			if strings.Contains(actionPhrase, " "+action+" ") {
				score += 20
				break
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
			if s.flat || !entry.listed {
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
			arguments, _ := json.Marshal(tool.InputSchema)
			lines = append(lines, fmt.Sprintf("%s (%s): %s\nArguments: %s", tool.Name, how, tool.Description, arguments))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %s (%s)", tool.Name, tool.Title, how))
		}
		items = append(items, item)
	}
	next := `Call one with run_tool {"name": ..., "arguments": {...}}`
	if s.flat {
		next = "Call the matching tool directly using its listed input schema."
	}
	return output{Text: strings.Join(lines, "\n"), Structured: map[string]any{
		"tools": items, "next": next,
	}}
}

func (s *Server) searchFor(caller Caller) *toolSearch {
	search := s.search
	if caller.DeployOnly {
		search = s.deploySearch
	}
	if s.fixed == nil && caller.Mode != CallerModeEvents && caller.Mode != CallerModeHackSite && flatListApp(caller.AppName) {
		copy := *search
		copy.flat = true
		copy.tools = append([]searchableTool(nil), search.tools...)
		for i := range copy.tools {
			copy.tools[i].listed = true
			copy.tools[i].tool = flatListTool(copy.tools[i].tool)
		}
		return &copy
	}
	return search
}
