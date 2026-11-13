package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type object struct{ Kind, Name, Slice, Note string }
type statement struct {
	Text string
	Line int
}

// splitSQL removes comments but preserves newlines and quoted bodies. PostgreSQL
// dollar quotes and nested block comments must not split a function at a semicolon.
func splitSQL(s string) []statement {
	var out []statement
	var b strings.Builder
	line, start := 1, 1
	quote, dollar, block := byte(0), "", 0
	comment := false
	flush := func() {
		if t := strings.TrimSpace(b.String()); t != "" {
			out = append(out, statement{t, start})
		}
		b.Reset()
		start = line
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\n' {
			line++
		}
		if comment {
			if c == '\n' {
				comment = false
				b.WriteByte(c)
			}
			i++
			continue
		}
		if block > 0 {
			if strings.HasPrefix(s[i:], "/*") {
				block++
				i += 2
			} else if strings.HasPrefix(s[i:], "*/") {
				block--
				i += 2
				b.WriteByte(' ')
			} else {
				if c == '\n' {
					b.WriteByte(c)
				}
				i++
			}
			continue
		}
		if dollar != "" {
			if strings.HasPrefix(s[i:], dollar) {
				b.WriteString(dollar)
				i += len(dollar)
				dollar = ""
			} else {
				b.WriteByte(c)
				i++
			}
			continue
		}
		if quote != 0 {
			b.WriteByte(c)
			i++
			if c == quote {
				if i < len(s) && s[i] == quote {
					b.WriteByte(s[i])
					i++
				} else {
					quote = 0
				}
			} else if c == '\\' && quote == '\'' && i < len(s) {
				b.WriteByte(s[i])
				i++
			}
			continue
		}
		if strings.HasPrefix(s[i:], "--") {
			comment = true
			b.WriteByte(' ')
			i += 2
			continue
		}
		if strings.HasPrefix(s[i:], "/*") {
			block = 1
			b.WriteByte(' ')
			i += 2
			continue
		}
		if strings.TrimSpace(b.String()) == "" && c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			start = line
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '$' {
			if m := dollarRE.FindString(s[i:]); m != "" {
				dollar = m
				b.WriteString(m)
				i += len(m)
				continue
			}
		}
		if c == ';' {
			flush()
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	flush()
	return out
}

var dollarRE = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*)?\$`)

const ident = `(?:public\.)?"?([a-z_][a-z0-9_]*)"?`

var createTableRE = regexp.MustCompile(`(?is)\bCREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` + ident)
var alterTableRE = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?` + ident)
var referencesRE = regexp.MustCompile(`(?i)\bREFERENCES\s+` + ident)
var functionRE = regexp.MustCompile(`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+` + ident)
var triggerRE = regexp.MustCompile(`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+` + ident + `.*?\bON\s+` + ident + `.*?\bEXECUTE\s+(?:FUNCTION|PROCEDURE)\s+` + ident)
var addColumnRE = regexp.MustCompile(`(?i)\bADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?"?([a-z_][a-z0-9_]*)"?\s+`)

type schemaInfo struct {
	Tables    map[string]bool
	Refs      map[string][]string
	Columns   map[string][]string
	Functions map[string]string
	Triggers  map[string][2]string
}

func parseSchema(s string) schemaInfo {
	z := schemaInfo{map[string]bool{}, map[string][]string{}, map[string][]string{}, map[string]string{}, map[string][2]string{}}
	for _, st := range splitSQL(s) {
		t := st.Text
		table := ""
		if m := createTableRE.FindStringSubmatch(t); m != nil {
			table = m[1]
			z.Tables[table] = true
			// Top-level comma-separated column definitions; commas in constraints/types are nested.
			p := strings.Index(t, "(")
			if p >= 0 {
				depth := 0
				begin := p + 1
				for i := p + 1; i < len(t); i++ {
					switch t[i] {
					case '(':
						depth++
					case ')':
						if depth > 0 {
							depth--
						} else {
							z.addColumn(table, t[begin:i])
							begin = i + 1
						}
					case ',':
						if depth == 0 {
							z.addColumn(table, t[begin:i])
							begin = i + 1
						}
					}
				}
			}
		} else if m := alterTableRE.FindStringSubmatch(t); m != nil {
			table = m[1]
			for _, c := range addColumnRE.FindAllStringSubmatch(t, -1) {
				z.Columns[c[1]] = append(z.Columns[c[1]], table+"."+c[1])
			}
		}
		if table != "" {
			for _, m := range referencesRE.FindAllStringSubmatch(t, -1) {
				z.Refs[table] = append(z.Refs[table], m[1])
			}
		}
		if m := functionRE.FindStringSubmatch(t); m != nil {
			z.Functions[m[1]] = t
		}
		if m := triggerRE.FindStringSubmatch(t); m != nil {
			z.Triggers[m[1]] = [2]string{m[2], m[3]}
		}
	}
	return z
}
func (z schemaInfo) addColumn(table, s string) {
	f := strings.Fields(s)
	if len(f) < 2 {
		return
	}
	n := strings.Trim(f[0], `"`)
	switch strings.ToUpper(n) {
	case "CONSTRAINT", "PRIMARY", "FOREIGN", "UNIQUE", "CHECK", "EXCLUDE":
		return
	}
	z.Columns[n] = append(z.Columns[n], table+"."+n)
}
func denied(n string) bool {
	return n == "site_events" || n == "site_event_deliveries" || n == "event_domains" || n == "paper_run" || strings.HasPrefix(n, "image_scan_")
}
func prefixHack(n string) bool {
	return !denied(n) && (n == "events" || strings.HasPrefix(n, "event_") || strings.HasPrefix(n, "hack_") || strings.HasPrefix(n, "paper_hack_"))
}
func sortedKeys[V any](m map[string]V) []string {
	a := make([]string, 0, len(m))
	for n := range m {
		a = append(a, n)
	}
	sort.Strings(a)
	return a
}
func unique(a []string) []string {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	return sortedKeys(m)
}
func proposeObjects(z schemaInfo) ([]object, []string) {
	h := map[string]bool{}
	notes := map[string]string{}
	var info []string
	for n := range z.Tables {
		if prefixHack(n) {
			h[n] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, n := range sortedKeys(z.Tables) {
			if h[n] || denied(n) || len(z.Refs[n]) == 0 {
				continue
			}
			all := true
			for _, r := range z.Refs[n] {
				all = all && h[r]
			}
			if all {
				h[n] = true
				changed = true
				notes[n] = "foreign keys reach only Hack tables: " + strings.Join(unique(z.Refs[n]), ", ")
				info = append(info, "added by foreign-key derivation: "+n+" -> "+strings.Join(unique(z.Refs[n]), ", "))
			}
		}
	}
	var out []object
	for _, n := range sortedKeys(h) {
		note := notes[n]
		if note == "" {
			note = "Hack table"
		}
		out = append(out, object{"table", n, "D3-S5c", note})
	}
	functions := map[string]bool{}
	for _, n := range sortedKeys(z.Triggers) {
		tr := z.Triggers[n]
		if h[tr[0]] {
			out = append(out, object{"trigger", n, "D3-S5c", "attached to " + tr[0] + "; executes " + tr[1]})
			functions[tr[1]] = true
		} else {
			for hn := range h {
				if len(occurrences(z.Functions[tr[1]], hn)) > 0 {
					info = append(info, "platform trigger naming Hack: "+n+" on "+tr[0])
					break
				}
			}
		}
	}
	for _, n := range sortedKeys(functions) {
		out = append(out, object{"function", n, "D3-S5c", "executed by Hack table trigger"})
		for _, pn := range sortedKeys(z.Tables) {
			if !h[pn] && len(occurrences(z.Functions[n], pn)) > 0 {
				info = append(info, "Hack function names platform table: "+n+" -> "+pn)
			}
		}
	}
	for _, n := range sortedKeys(z.Tables) {
		if h[n] {
			continue
		}
		yes, no := false, false
		for _, r := range z.Refs[n] {
			yes = yes || h[r]
			no = no || !h[r]
		}
		if yes && no {
			info = append(info, "references Hack and platform tables, for a person to decide: "+n+" -> "+strings.Join(unique(z.Refs[n]), ", "))
		}
	}
	sort.Strings(info)
	return out, info
}
func readObjects(path string) ([]object, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var out []object
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		p := strings.SplitN(l, "|", 4)
		if len(p) != 4 {
			return nil, fmt.Errorf("%s:%d: expected four fields", path, i+1)
		}
		for j := range p {
			p[j] = strings.TrimSpace(p[j])
		}
		switch p[0] {
		case "table", "function", "trigger", "column":
		default:
			return nil, fmt.Errorf("%s:%d: invalid kind", path, i+1)
		}
		out = append(out, object{p[0], p[1], p[2], p[3]})
	}
	return out, nil
}
func objectName(o object) string {
	if o.Kind == "column" {
		return o.Name[strings.LastIndex(o.Name, ".")+1:]
	}
	return o.Name
}
func identifier(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// occurrences folds unquoted identifiers to lower case; columns allow any qualifier.
func occurrences(s, n string, column ...bool) []int {
	var out []int
	if n == "" {
		return out
	}
	for _, m := range wordsRE.FindAllStringIndex(s, -1) {
		i, end := m[0], m[1]
		if !strings.EqualFold(s[i:end], n) {
			continue
		}
		left, right := i, end
		if left > 0 && s[left-1] == '"' && right < len(s) && s[right] == '"' {
			if s[i:end] != n {
				continue
			}
			left--
			right++
		}
		blocked := func(c byte) bool { return c == '/' || c == '-' }
		if left > 0 && blocked(s[left-1]) && !strings.HasSuffix(s[:left], "*/") ||
			right < len(s) && blocked(s[right]) && !strings.HasPrefix(s[right:], "--") && !strings.HasPrefix(s[right:], "/*") {
			continue
		}
		if left > 0 && s[left-1] == '.' && !(len(column) > 0 && column[0]) {
			prefix := s[:left-1]
			schema := "public"
			if strings.HasSuffix(strings.ToLower(prefix), `"public"`) {
				schema = `"public"`
			}
			start := len(prefix) - len(schema)
			if start < 0 || !strings.EqualFold(prefix[start:], schema) || schema == `"public"` && prefix[start:] != schema || start > 0 && (identifier(prefix[start-1]) || prefix[start-1] == '.') {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}
