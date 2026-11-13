package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/db/migrations"
)

type migration struct {
	Name, Owner, Reason       string
	CreatedPlatform, Mentions []string
}

var actionRE = regexp.MustCompile(`(?is)\b(CREATE|ALTER|DROP)\s+(?:OR\s+REPLACE\s+)?(?:UNIQUE\s+)?(TABLE|FUNCTION|TRIGGER|INDEX)\s+(?:CONCURRENTLY\s+)?(?:IF\s+(?:NOT\s+)?EXISTS\s+)?` + ident)
var onRE = regexp.MustCompile(`(?i)\bON\s+(?:ONLY\s+)?` + ident)
var writeRE = regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE(?:\s+TABLE)?)\s+(?:ONLY\s+)?` + ident)

func migrationOrder(root string) ([]string, map[string]bool, error) {
	entries, e := os.ReadDir(filepath.Join(root, "db/migrations"))
	if e != nil {
		return nil, nil, e
	}
	present := map[string]bool{}
	for _, en := range entries {
		if !en.IsDir() && (strings.HasSuffix(en.Name(), ".sql") || en.Name() == "0c2-diskmove.sh") {
			present[en.Name()] = true
		}
	}
	var out []string
	baseline := map[string]bool{}
	for n := range migrations.Baseline {
		if present[n] {
			baseline[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	var withShell []string
	for _, n := range out {
		withShell = append(withShell, n)
		delete(present, n)
		if n == "0c1-handles.sql" && present["0c2-diskmove.sh"] {
			withShell = append(withShell, "0c2-diskmove.sh")
			delete(present, "0c2-diskmove.sh")
		}
	}
	out = withShell
	for _, n := range (migrations.Set{Platform: migrations.Embedded()}).Tracked() {
		if present[n] {
			out = append(out, n)
			delete(present, n)
		}
	}
	out = append(out, sortedKeys(present)...)
	return out, baseline, nil
}
func proposeMigrations(root string, order []string, objects []object, z schemaInfo) ([]migration, error) {
	hack := map[string]bool{}
	known := map[string]bool{}
	for n := range z.Tables {
		known[n] = true
	}
	for n := range z.Functions {
		known[n] = true
	}
	for n := range z.Triggers {
		known[n] = true
	}
	for _, o := range objects {
		hack[o.Name] = true
		if o.Kind != "column" {
			known[o.Name] = true
		}
	}
	var out []migration
	for _, n := range order {
		m := migration{Name: n}
		if strings.HasSuffix(n, ".sh") {
			m.Owner = "platform?"
			m.Reason = "shell step, not SQL: read by a person"
			out = append(out, m)
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, "db/migrations", n))
		if e != nil {
			return nil, e
		}
		ha, pa, reads, mentions := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
		created := map[string]bool{}
		touchHack := false
		for _, st := range splitSQL(string(b)) {
			t := st.Text
			for name := range known {
				if len(occurrences(withoutColumnNames(t), name)) > 0 {
					mentions[name] = true
					if hack[name] {
						touchHack = true
					} else if z.Tables[name] {
						reads[name] = true
					}
				}
			}
			for _, o := range objects {
				if o.Kind == "column" && len(occurrences(t, objectName(o), true)) > 0 {
					touchHack = true
				}
			}
			mark := func(action, name string, create bool) {
				mentions[name] = true
				if hack[name] {
					touchHack = true
					ha[action+" "+name] = true
				} else {
					pa[action+" "+name] = true
					if create {
						created[name] = true
					}
				}
				delete(reads, name)
			}
			for _, a := range actionRE.FindAllStringSubmatch(t, -1) {
				verb, kind, name := strings.ToLower(a[1]), strings.ToUpper(a[2]), a[3]
				mentions[a[3]] = true
				if kind == "INDEX" || kind == "TRIGGER" {
					if on := onRE.FindStringSubmatch(t); on != nil {
						name = on[1]
					} else {
						continue
					}
				}
				action := map[string]string{"create": "creates", "alter": "alters", "drop": "drops"}[verb]
				if kind == "INDEX" || kind == "TRIGGER" {
					action += " " + strings.ToLower(kind) + " on"
				}
				mark(action, name, false)
				if verb == "create" && !hack[name] {
					created[a[3]] = true
				}
			}
			// Function bodies and data-only migrations contribute writes too.
			for _, a := range writeRE.FindAllStringSubmatch(t, -1) {
				name := a[2]
				if !known[name] {
					continue
				}
				action := "writes"
				mark(action, name, false)
			}
		}
		if !touchHack {
			m.Owner = "platform"
		} else if len(pa) == 0 {
			m.Owner = "hack"
		} else {
			m.Owner = "mixed?"
		}
		var reason []string
		for _, a := range sortedKeys(pa) {
			if touchHack {
				a += " (platform)"
			}
			reason = append(reason, a)
		}
		for _, a := range sortedKeys(ha) {
			reason = append(reason, a+" (hack)")
		}
		if touchHack && len(ha) == 0 {
			var ns []string
			for n := range mentions {
				if hack[n] {
					ns = append(ns, n)
				}
			}
			sort.Strings(ns)
			if len(ns) > 0 {
				reason = append(reason, "reads "+strings.Join(ns, ", ")+" (hack)")
			} else {
				reason = append(reason, "uses Hack-owned column")
			}
		}
		if len(reads) > 0 {
			reason = append(reason, "reads "+strings.Join(sortedKeys(reads), ", "))
		}
		if len(reason) == 0 {
			reason = append(reason, "SQL configuration; no schema objects")
		}
		m.Reason = strings.Join(reason, "; ")
		m.CreatedPlatform = sortedKeys(created)
		m.Mentions = sortedKeys(mentions)
		out = append(out, m)
	}
	return out, nil
}
func mixedComment(m migration, all []migration, confirmed map[string]migration, baseline map[string]bool) string {
	var later []string
	after := false
	for _, n := range all {
		isLater := after || baseline[m.Name] && baseline[n.Name] && m.Name != n.Name
		if n.Name == m.Name {
			after = true
		}
		if !isLater || confirmed[n.Name].Owner != "platform" {
			continue
		}
		found := false
		for _, o := range m.CreatedPlatform {
			for _, x := range n.Mentions {
				found = found || x == o
			}
		}
		if found {
			later = append(later, n.Name)
		}
	}
	objects := "none"
	if len(m.CreatedPlatform) > 0 {
		objects = strings.Join(m.CreatedPlatform, ", ")
	}
	deps := "none"
	if len(later) > 0 {
		deps = strings.Join(later, ", ")
	}
	return "#   mixed: platform objects created: " + objects + "; later platform files that mention them: " + deps
}
func checkMigrations(path string, all []migration, baseline map[string]bool, fix bool) (string, int, error) {
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return "", 1, e
	}
	lines := strings.Split(string(b), "\n")
	confirmed := map[string]migration{}
	var messages []string
	code := 0
	indices := map[string]int{}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		p := strings.SplitN(l, "|", 3)
		if len(p) != 3 {
			messages = append(messages, fmt.Sprintf("line %d: expected file | owner | reason", i+1))
			code = 1
			continue
		}
		for j := range p {
			p[j] = strings.TrimSpace(p[j])
		}
		if _, ok := confirmed[p[0]]; ok {
			messages = append(messages, "duplicate file: "+p[0])
			code = 1
		}
		confirmed[p[0]] = migration{Name: p[0], Owner: p[1], Reason: p[2]}
		indices[p[0]] = i
		if p[1] != "platform" && p[1] != "hack" && p[1] != "mixed" {
			messages = append(messages, "invalid owner: "+p[0]+": "+p[1])
			code = 1
		}
		if p[2] == "" {
			messages = append(messages, "empty reason: "+p[0])
			code = 1
		}
	}
	present := map[string]bool{}
	expected := map[int]string{}
	for _, m := range all {
		present[m.Name] = true
		c, ok := confirmed[m.Name]
		if !ok {
			messages = append(messages, "missing file: "+m.Name)
			code = 1
			continue
		}
		if c.Owner != m.Owner {
			messages = append(messages, m.Name+": confirmed "+c.Owner+"; proposal "+m.Owner+" | "+m.Reason)
		}
		if c.Owner == "mixed" {
			comment := mixedComment(m, all, confirmed, baseline)
			messages = append(messages, m.Name+": "+comment)
			idx := indices[m.Name]
			expected[idx] = comment
			if idx+1 >= len(lines) || lines[idx+1] != comment {
				if !fix {
					messages = append(messages, "missing or stale mixed comment; expected: "+comment)
					code = 1
				}
			}
		}
	}
	for _, n := range sortedKeys(confirmed) {
		if !present[n] {
			messages = append(messages, "file does not exist: "+n)
			code = 1
		}
	}
	// A generated line is valid only directly beneath its mixed entry.
	for i, l := range lines {
		if strings.HasPrefix(l, "#   mixed:") && (i == 0 || expected[i-1] != l) {
			if !fix {
				messages = append(messages, "stale mixed comment: "+l)
				code = 1
			}
		}
	}
	if fix {
		var rewritten []string
		for i, l := range lines {
			if strings.HasPrefix(l, "#   mixed:") {
				continue
			}
			rewritten = append(rewritten, l)
			if c := expected[i]; c != "" {
				rewritten = append(rewritten, c)
			}
		}
		if e := os.WriteFile(path, []byte(strings.Join(rewritten, "\n")), 0644); e != nil {
			return "", 1, e
		}
	}
	return strings.Join(messages, "\n") + "\n", code, nil
}
func migrationColumns(root string, z schemaInfo) ([]string, error) {
	proposed, _ := proposeObjects(z)
	hackTables := map[string]bool{}
	for _, ob := range proposed {
		if ob.Kind == "table" {
			hackTables[ob.Name] = true
		}
	}
	files, e := filepath.Glob(filepath.Join(root, "db/migrations/*hack*.sql"))
	if e != nil {
		return nil, e
	}
	var out []string
	for _, file := range files {
		b, e := os.ReadFile(file)
		if e != nil {
			return nil, e
		}
		for _, st := range splitSQL(string(b)) {
			m := alterTableRE.FindStringSubmatch(st.Text)
			if m == nil || hackTables[m[1]] {
				continue
			}
			for _, c := range addColumnRE.FindAllStringSubmatch(st.Text, -1) {
				out = append(out, m[1]+"."+c[1]+" | "+filepath.Base(file))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Mask only the first word of each CREATE TABLE column definition, keeping
// references and expressions available to the ownership proposal.
func withoutColumnNames(s string) string {
	m := createTableRE.FindStringIndex(s)
	if m == nil {
		return s
	}
	start := strings.Index(s[m[1]:], "(")
	if start < 0 {
		return s
	}
	start += m[1]
	b := []byte(s)
	depth, first := 1, true
	quote := byte(0)
	for i := start + 1; i < len(s) && depth > 0; i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if depth == 1 && first {
			if c == ' ' || c == '\n' || c == '\t' || c == '\r' {
				continue
			}
			end := i
			if c == '"' {
				end++
				for end < len(s) && s[end] != '"' {
					end++
				}
				if end < len(s) {
					end++
				}
			} else {
				for end < len(s) && identifier(s[end]) {
					end++
				}
			}
			word := strings.ToUpper(strings.Trim(s[i:end], `"`))
			switch word {
			case "CONSTRAINT", "PRIMARY", "FOREIGN", "UNIQUE", "CHECK", "EXCLUDE":
			default:
				for j := i; j < end; j++ {
					b[j] = ' '
				}
			}
			first = false
			if end > i {
				i = end - 1
				continue
			}
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 1 {
				first = true
			}
		}
	}
	return string(b)
}
