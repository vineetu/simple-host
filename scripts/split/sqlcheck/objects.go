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

// sqlSplitter is splitSQL's position in the text: the statement being built,
// its first line, and which comment or quoted body it is inside.
type sqlSplitter struct {
	s           string
	i           int
	out         []statement
	b           strings.Builder
	line, start int
	quote       byte
	dollar      string
	block       int
	comment     bool
}

// splitSQL removes comments but preserves newlines and quoted bodies. PostgreSQL
// dollar quotes and nested block comments must not split a function at a semicolon.
func splitSQL(s string) []statement {
	p := &sqlSplitter{s: s, line: 1, start: 1}
	for p.i < len(s) {
		c := s[p.i]
		if c == '\n' {
			p.line++
		}
		if !p.inside(c) {
			p.plain(c)
		}
	}
	p.flush()
	return p.out
}

// flush ends the current statement, keeping it when it is not blank.
func (p *sqlSplitter) flush() {
	if t := strings.TrimSpace(p.b.String()); t != "" {
		p.out = append(p.out, statement{t, p.start})
	}
	p.b.Reset()
	p.start = p.line
}

// inside consumes c when it is within a line comment, block comment, dollar
// quote or quoted string, and reports whether it was.
func (p *sqlSplitter) inside(c byte) bool {
	switch {
	case p.comment:
		if c == '\n' {
			p.comment = false
			p.b.WriteByte(c)
		}
		p.i++
	case p.block > 0:
		p.blockComment(c)
	case p.dollar != "":
		if strings.HasPrefix(p.s[p.i:], p.dollar) {
			p.b.WriteString(p.dollar)
			p.i += len(p.dollar)
			p.dollar = ""
		} else {
			p.b.WriteByte(c)
			p.i++
		}
	case p.quote != 0:
		p.quoted(c)
	default:
		return false
	}
	return true
}

// blockComment consumes c inside a block comment, which may nest; only
// newlines are kept, and the comment as a whole becomes one space.
func (p *sqlSplitter) blockComment(c byte) {
	if strings.HasPrefix(p.s[p.i:], "/*") {
		p.block++
		p.i += 2
	} else if strings.HasPrefix(p.s[p.i:], "*/") {
		p.block--
		p.i += 2
		p.b.WriteByte(' ')
	} else {
		if c == '\n' {
			p.b.WriteByte(c)
		}
		p.i++
	}
}

// quoted keeps c inside a quoted string, where a doubled quote and a
// backslash escape in a single-quoted string do not end it.
func (p *sqlSplitter) quoted(c byte) {
	p.b.WriteByte(c)
	p.i++
	if c == p.quote {
		if p.i < len(p.s) && p.s[p.i] == p.quote {
			p.b.WriteByte(p.s[p.i])
			p.i++
		} else {
			p.quote = 0
		}
	} else if c == '\\' && p.quote == '\'' && p.i < len(p.s) {
		p.b.WriteByte(p.s[p.i])
		p.i++
	}
}

// plain handles c outside any comment or quote: it may open one, end the
// statement at a semicolon, or is kept.
func (p *sqlSplitter) plain(c byte) {
	if strings.HasPrefix(p.s[p.i:], "--") {
		p.comment = true
		p.b.WriteByte(' ')
		p.i += 2
		return
	}
	if strings.HasPrefix(p.s[p.i:], "/*") {
		p.block = 1
		p.b.WriteByte(' ')
		p.i += 2
		return
	}
	if strings.TrimSpace(p.b.String()) == "" && c != ' ' && c != '\n' && c != '\t' && c != '\r' {
		p.start = p.line
	}
	if c == '\'' || c == '"' {
		p.quote = c
	}
	if c == '$' {
		if m := dollarRE.FindString(p.s[p.i:]); m != "" {
			p.dollar = m
			p.b.WriteString(m)
			p.i += len(m)
			return
		}
	}
	if c == ';' {
		p.flush()
		p.i++
		return
	}
	p.b.WriteByte(c)
	p.i++
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
	h, notes, info := deriveHackTables(z)
	var out []object
	for _, n := range sortedKeys(h) {
		note := notes[n]
		if note == "" {
			note = "Hack table"
		}
		out = append(out, object{"table", n, "D3-S5c", note})
	}
	triggers, functions, triggerInfo := hackTriggers(z, h)
	out = append(out, triggers...)
	info = append(info, triggerInfo...)
	for _, n := range sortedKeys(functions) {
		out = append(out, object{"function", n, "D3-S5c", "executed by Hack table trigger"})
		for _, pn := range sortedKeys(z.Tables) {
			if !h[pn] && len(occurrences(z.Functions[n], pn)) > 0 {
				info = append(info, "Hack function names platform table: "+n+" -> "+pn)
			}
		}
	}
	info = append(info, mixedReferences(z, h)...)
	sort.Strings(info)
	return out, info
}

// deriveHackTables picks the Hack tables: those with a Hack prefix, then,
// until nothing changes, any table whose foreign keys all reach Hack tables.
func deriveHackTables(z schemaInfo) (h map[string]bool, notes map[string]string, info []string) {
	h, notes = map[string]bool{}, map[string]string{}
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
	return h, notes, info
}

// hackTriggers proposes the triggers on Hack tables and the functions they
// execute, and notes platform triggers whose function names a Hack table.
func hackTriggers(z schemaInfo, h map[string]bool) (out []object, functions map[string]bool, info []string) {
	functions = map[string]bool{}
	for _, n := range sortedKeys(z.Triggers) {
		tr := z.Triggers[n]
		if h[tr[0]] {
			out = append(out, object{"trigger", n, "D3-S5c", "attached to " + tr[0] + "; executes " + tr[1]})
			functions[tr[1]] = true
			continue
		}
		for hn := range h {
			if len(occurrences(z.Functions[tr[1]], hn)) > 0 {
				info = append(info, "platform trigger naming Hack: "+n+" on "+tr[0])
				break
			}
		}
	}
	return out, functions, info
}

// mixedReferences notes platform tables with foreign keys to both Hack and
// platform tables, which a person must decide.
func mixedReferences(z schemaInfo, h map[string]bool) []string {
	var info []string
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
	return info
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
		if joinedBySlashOrDash(s, left, right) {
			continue
		}
		if left > 0 && s[left-1] == '.' && !(len(column) > 0 && column[0]) && !publicQualified(s, left) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// joinedBySlashOrDash reports whether the word s[left:right] touches a / or -,
// as in a path or hyphenated word, other than a comment marker.
func joinedBySlashOrDash(s string, left, right int) bool {
	blocked := func(c byte) bool { return c == '/' || c == '-' }
	return left > 0 && blocked(s[left-1]) && !strings.HasSuffix(s[:left], "*/") ||
		right < len(s) && blocked(s[right]) && !strings.HasPrefix(s[right:], "--") && !strings.HasPrefix(s[right:], "/*")
}

// publicQualified reports whether the qualifier before the dot ending at
// left is the public schema itself, quoted or not.
func publicQualified(s string, left int) bool {
	prefix := s[:left-1]
	schema := "public"
	if strings.HasSuffix(strings.ToLower(prefix), `"public"`) {
		schema = `"public"`
	}
	start := len(prefix) - len(schema)
	return !(start < 0 || !strings.EqualFold(prefix[start:], schema) || schema == `"public"` && prefix[start:] != schema || start > 0 && (identifier(prefix[start-1]) || prefix[start-1] == '.'))
}
