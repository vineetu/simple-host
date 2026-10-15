package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Page checks run after create_site and update_site publish. They catch the
// mistakes that leave a page looking fine but saving nothing: SH used without
// auth.js, auth.js deferred under a script that calls SH at once, a script
// sent double-escaped, and storage names the site does not have. They only
// add warnings to the answer; the deploy itself is unchanged.

var (
	shUse          = regexp.MustCompile(`\bSH\.(storage|mount|requireSignIn|me|signIn|signOut|email)\b`)
	authTag        = regexp.MustCompile(`(?is)<script[^>]*\ssrc=["']([^"']*/)?auth\.js(\?[^"']*)?["'][^>]*>`)
	inlineScript   = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script>`)
	hiddenID       = regexp.MustCompile(`(?is)<[a-z][a-z0-9]*\s[^>]*\bid=["']([A-Za-z][\w-]*)["'][^>]*\shidden[\s>/=]`)
	srcAttr        = regexp.MustCompile(`(^|\s)src=`)
	scriptSrc      = regexp.MustCompile(`(?is)<script[^>]*\ssrc=["']([^"']+)["']`)
	doubleEscaped  = regexp.MustCompile(`[;{}]\\n(\s{2,}|\\t|[A-Za-z])`)
	storageRef     = regexp.MustCompile(`SH\.storage\.(sqlite|kv|files)\(\s*["']([A-Za-z0-9_-]+)["']\s*\)(\s*\.table\(\s*["']([A-Za-z0-9_]+)["']\s*\))?`)
	tableRef       = regexp.MustCompile(`\.table\(\s*["']([A-Za-z0-9_]+)["']\s*\)`)
	pageSQL        = regexp.MustCompile(`(?i)\.(query|execute)\(\s*["'\x60]\s*(SELECT|INSERT|UPDATE|DELETE|REPLACE|WITH)\b`)
	waitsForLoaded = regexp.MustCompile(`DOMContentLoaded|addEventListener\(\s*["']load["']|onload\s*=|SH\.ready`)
)

const (
	maxPageWarnings   = 6
	maxPageCheckBytes = 512 << 10 // larger files (bundles) are not scanned
)

// pageWarnings returns plain warnings about the files just published to site.
func pageWarnings(c *call, site string, files map[string]string) []string {
	var warns []string
	// Each warning stands alone: some apps show only the structured answer.
	add := func(format string, a ...any) {
		if len(warns) < maxPageWarnings {
			warns = append(warns, "Not working yet, fix it and publish again: "+fmt.Sprintf(format, a...))
		}
	}
	type use struct{ kind, name string }
	uses := map[use]map[string]bool{}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		lower := strings.ToLower(p)
		isHTML := strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
		if !isHTML && !strings.HasSuffix(lower, ".js") {
			continue
		}
		text := files[p]
		if len(text) > maxPageCheckBytes {
			continue
		}
		code := text
		if isHTML {
			// The page's own scripts, inline and local files it loads.
			var b strings.Builder
			for _, m := range inlineScript.FindAllStringSubmatch(text, -1) {
				attrs := strings.ToLower(m[1])
				if srcAttr.MatchString(attrs) || (strings.Contains(attrs, "type=") && !strings.Contains(attrs, "javascript") && !strings.Contains(attrs, "module")) {
					continue
				}
				b.WriteString(m[2])
				b.WriteString("\n")
			}
			code = b.String()
			// The page's own local scripts, each once and only if small.
			local := code
			seen := map[string]bool{}
			for _, m := range scriptSrc.FindAllStringSubmatch(text, -1) {
				src := strings.TrimPrefix(strings.SplitN(m[1], "?", 2)[0], "./")
				src = strings.TrimPrefix(src, "/")
				if js, ok := files[src]; ok && !seen[src] && len(js) <= maxPageCheckBytes && len(local)+len(js) <= 2*maxPageCheckBytes {
					seen[src] = true
					local += "\n" + js
				}
			}
			tag := authTag.FindString(text)
			if shUse.MatchString(local) && tag == "" {
				add(instanceText(`%s uses SH but never loads auth.js, so nothing on it can sign in or save. Add <script src="https://simple-host.app/auth.js"></script> in the head, before the page's own scripts.`), p)
			}
			if t := strings.ToLower(tag); (strings.Contains(t, "defer") || strings.Contains(t, "async")) && shUse.MatchString(code) && !waitsForLoaded.MatchString(code) {
				add(`%s loads auth.js with defer or async, but its script calls SH straight away, before auth.js has run ("SH is not defined"). Load auth.js with a plain script tag in the head, without defer.`, p)
			}
		}
		if isHTML {
			// A hidden element that CSS also sets to display: none never shows,
			// whatever the script does with .hidden.
			for _, m := range hiddenID.FindAllStringSubmatch(text, 20) {
				rule := regexp.MustCompile(`(?is)#` + regexp.QuoteMeta(m[1]) + `\s*\{[^}]*display\s*:\s*none`)
				if rule.MatchString(text) && strings.Contains(code, ".hidden") {
					add(`%s: #%s has the hidden attribute and a CSS rule display: none, so setting .hidden = false never shows it. Remove display: none from that rule.`, p, m[1])
				}
			}
		}
		if pageSQL.MatchString(code) {
			add(`%s sends SQL from the page (query or execute), which a page may not do: use SH.storage.sqlite(name).table(t).list, .get, .add, .edit and .delete (get_page_recipe shows them); SQL is for your storage_sql_* tools only.`, p)
		}
		// Double-escaped files arrive as one line with literal \n in place of line breaks.
		if strings.Count(text, "\n") < 3 && doubleEscaped.MatchString(code) {
			add(`%s has a script with literal \n sequences between statements: it was sent double-escaped and will not run. Send the file with real line breaks.`, p)
		}
		for _, m := range storageRef.FindAllStringSubmatch(code, -1) {
			u := use{m[1], m[2]}
			if uses[u] == nil {
				uses[u] = map[string]bool{}
			}
			if m[4] != "" {
				uses[u][m[4]] = true
			}
		}
		// .table("t") on a variable: credit it to the page's only SQLite resource.
		var sqlite []use
		for u := range uses {
			if u.kind == "sqlite" && (strings.Contains(code, `"`+u.name+`"`) || strings.Contains(code, `'`+u.name+`'`)) {
				sqlite = append(sqlite, u)
			}
		}
		if len(sqlite) == 1 {
			for _, m := range tableRef.FindAllStringSubmatch(code, -1) {
				uses[sqlite[0]][m[1]] = true
			}
		}
	}
	if len(uses) == 0 || len(warns) >= maxPageWarnings {
		return warns
	}
	base := "/v1/sites/" + url.PathEscape(site) + "/storage"
	res := c.do(http.MethodGet, base+"/resources", nil, nil)
	if !res.ok() {
		return warns
	}
	var listed struct {
		Resources []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"resources"`
	}
	if json.Unmarshal(res.body, &listed) != nil {
		return warns
	}
	kinds := map[string]string{}
	for _, r := range listed.Resources {
		kinds[r.Name] = r.Kind
	}
	keys := make([]use, 0, len(uses))
	for u := range uses {
		keys = append(keys, u)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].name < keys[j].name })
	lookups := 0
	for _, u := range keys {
		kind, ok := kinds[u.name]
		switch {
		case !ok:
			setup := "storage_set_resource"
			if u.kind == "sqlite" {
				setup += " and its tables with storage_sql_schema"
			}
			add(`The page uses the %s resource %q, which this site does not have yet: create it with %s (get_page_recipe shows the calls), or the page cannot save or show anything.`, u.kind, u.name, setup)
		case kind != u.kind:
			add(`The page uses %q as %s, but it is a %s resource on this site: change the page to SH.storage.%s(%q).`, u.name, u.kind, kind, kind, u.name)
		case u.kind == "sqlite" && len(uses[u]) > 0 && lookups < 5:
			lookups++
			body, _ := json.Marshal(map[string]any{"sql": "SELECT name FROM sqlite_master WHERE type = 'table'", "params": []any{}})
			q := c.do(http.MethodPost, base+"/sqlite/"+url.PathEscape(u.name)+"/query", body, nil)
			var rows struct {
				Rows [][]any `json:"rows"`
			}
			if !q.ok() || json.Unmarshal(q.body, &rows) != nil {
				continue
			}
			have := map[string]bool{}
			var names []string
			for _, r := range rows.Rows {
				if len(r) > 0 {
					if n, ok := r[0].(string); ok && !strings.HasPrefix(n, "sqlite_") {
						have[strings.ToLower(n)] = true
						names = append(names, n)
					}
				}
			}
			var missing []string
			for t := range uses[u] {
				if !have[strings.ToLower(t)] {
					missing = append(missing, t)
				}
			}
			sort.Strings(missing)
			for _, t := range missing {
				existing := "no tables yet"
				if len(names) > 0 {
					existing = "tables: " + strings.Join(names, ", ")
				}
				add(`The page uses table %q in %q, which does not exist (%s): create it with storage_sql_schema, or fix the name in the page.`, t, u.name, existing)
			}
		}
	}
	return warns
}
