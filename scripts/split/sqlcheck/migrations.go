package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/db/migrations"
	hackmigrations "github.com/vsriram/simple-host/products/hack/db/migrations"
)

type migration struct {
	Name, Owner, Reason       string
	CreatedPlatform, Mentions []string
}

var actionRE = regexp.MustCompile(`(?is)\b(CREATE|ALTER|DROP)\s+(?:OR\s+REPLACE\s+)?(?:UNIQUE\s+)?(TABLE|FUNCTION|TRIGGER|INDEX)\s+(?:CONCURRENTLY\s+)?(?:IF\s+(?:NOT\s+)?EXISTS\s+)?` + ident)
var onRE = regexp.MustCompile(`(?i)\bON\s+(?:ONLY\s+)?` + ident)
var writeRE = regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE(?:\s+TABLE)?)\s+(?:ONLY\s+)?` + ident)

func migrationOrder(root string) ([]string, map[string]bool, error) {
	entries, e := os.ReadDir(filepath.Join(root, "host/db/migrations"))
	if e != nil {
		return nil, nil, e
	}
	present := map[string]bool{}
	for _, en := range entries {
		if !en.IsDir() && (strings.HasSuffix(en.Name(), ".sql") || en.Name() == "0c2-diskmove.sh") {
			present[en.Name()] = true
		}
	}
	if product, err := os.ReadDir(filepath.Join(root, "products/hack/db/migrations")); err == nil {
		for _, entry := range product {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
				present[entry.Name()] = true
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
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
	product := hackmigrations.Embedded()
	for _, n := range (migrations.Set{Platform: migrations.Embedded(), Product: &product, ProductOrder: hackmigrations.ProductOrder}).Tracked() {
		if present[n] {
			out = append(out, n)
			delete(present, n)
		}
	}
	out = append(out, sortedKeys(present)...)
	return out, baseline, nil
}

// migrationIndex is what a proposal matches against: the Hack object names,
// and every name that is a schema object rather than a column.
func migrationIndex(objects []object, z schemaInfo) (hack, known map[string]bool) {
	hack, known = map[string]bool{}, map[string]bool{}
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
	return hack, known
}

// readMigration reads a migration from the platform directory, or from the
// product directory when the platform has no such file.
func readMigration(root, n string) ([]byte, error) {
	b, e := os.ReadFile(filepath.Join(root, "host/db/migrations", n))
	if os.IsNotExist(e) {
		b, e = os.ReadFile(filepath.Join(root, "products/hack/db/migrations", n))
	}
	return b, e
}

func proposeMigrations(root string, order []string, objects []object, z schemaInfo) ([]migration, error) {
	hack, known := migrationIndex(objects, z)
	var out []migration
	for _, n := range order {
		if strings.HasSuffix(n, ".sh") {
			out = append(out, migration{Name: n, Owner: "platform?", Reason: "shell step, not SQL: read by a person"})
			continue
		}
		b, e := readMigration(root, n)
		if e != nil {
			return nil, e
		}
		u := &migrationUses{hack: hack, known: known, ha: map[string]bool{}, pa: map[string]bool{}, reads: map[string]bool{}, mentions: map[string]bool{}, created: map[string]bool{}}
		for _, st := range splitSQL(string(b)) {
			u.statement(st.Text, objects, z)
		}
		out = append(out, u.proposal(n))
	}
	return out, nil
}

// migrationUses collects what one migration does: Hack and platform actions,
// platform tables it only reads, every name it mentions, the platform
// objects it creates, and whether it touches Hack at all.
type migrationUses struct {
	hack, known                      map[string]bool
	ha, pa, reads, mentions, created map[string]bool
	touchHack                        bool
}

// statement records one SQL statement's mentions, schema actions and writes.
func (u *migrationUses) statement(t string, objects []object, z schemaInfo) {
	for name := range u.known {
		if len(occurrences(withoutColumnNames(t), name)) > 0 {
			u.mentions[name] = true
			if u.hack[name] {
				u.touchHack = true
			} else if z.Tables[name] {
				u.reads[name] = true
			}
		}
	}
	for _, o := range objects {
		if o.Kind == "column" && len(occurrences(t, objectName(o), true)) > 0 {
			u.touchHack = true
		}
	}
	u.schemaActions(t)
	// Function bodies and data-only migrations contribute writes too.
	for _, a := range writeRE.FindAllStringSubmatch(t, -1) {
		if u.known[a[2]] {
			u.mark("writes", a[2])
		}
	}
}

// schemaActions records each CREATE, ALTER or DROP in t; an index or trigger
// counts against the table it is on.
func (u *migrationUses) schemaActions(t string) {
	for _, a := range actionRE.FindAllStringSubmatch(t, -1) {
		verb, kind, name := strings.ToLower(a[1]), strings.ToUpper(a[2]), a[3]
		u.mentions[a[3]] = true
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
		u.mark(action, name)
		if verb == "create" && !u.hack[name] {
			u.created[a[3]] = true
		}
	}
}

// mark records action on name as a Hack or platform action; a name acted on
// is no longer only read.
func (u *migrationUses) mark(action, name string) {
	u.mentions[name] = true
	if u.hack[name] {
		u.touchHack = true
		u.ha[action+" "+name] = true
	} else {
		u.pa[action+" "+name] = true
	}
	delete(u.reads, name)
}

// proposal names the owner the uses point to, and the reason that shows why.
func (u *migrationUses) proposal(n string) migration {
	m := migration{Name: n}
	if !u.touchHack {
		m.Owner = "platform"
	} else if len(u.pa) == 0 {
		m.Owner = "hack"
	} else {
		m.Owner = "mixed?"
	}
	var reason []string
	for _, a := range sortedKeys(u.pa) {
		if u.touchHack {
			a += " (platform)"
		}
		reason = append(reason, a)
	}
	for _, a := range sortedKeys(u.ha) {
		reason = append(reason, a+" (hack)")
	}
	if u.touchHack && len(u.ha) == 0 {
		reason = append(reason, u.hackReadReason())
	}
	if len(u.reads) > 0 {
		reason = append(reason, "reads "+strings.Join(sortedKeys(u.reads), ", "))
	}
	if len(reason) == 0 {
		reason = append(reason, "SQL configuration; no schema objects")
	}
	m.Reason = strings.Join(reason, "; ")
	m.CreatedPlatform = sortedKeys(u.created)
	m.Mentions = sortedKeys(u.mentions)
	return m
}

// hackReadReason explains a migration that touches Hack without acting on a
// Hack object: the Hack names it reads, or a Hack-owned column.
func (u *migrationUses) hackReadReason() string {
	var ns []string
	for n := range u.mentions {
		if u.hack[n] {
			ns = append(ns, n)
		}
	}
	sort.Strings(ns)
	if len(ns) > 0 {
		return "reads " + strings.Join(ns, ", ") + " (hack)"
	}
	return "uses Hack-owned column"
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

// migrationCheck compares migration-owners.txt with the proposals, collecting
// messages; any failing message sets code to 1.
type migrationCheck struct {
	lines     []string
	confirmed map[string]migration
	indices   map[string]int
	expected  map[int]string
	messages  []string
	code      int
	fix       bool
}

func (c *migrationCheck) note(msg string) { c.messages = append(c.messages, msg) }
func (c *migrationCheck) fail(msg string) { c.note(msg); c.code = 1 }

func checkMigrations(path string, all []migration, baseline map[string]bool, fix bool) (string, int, error) {
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return "", 1, e
	}
	c := &migrationCheck{lines: strings.Split(string(b), "\n"), confirmed: map[string]migration{}, indices: map[string]int{}, expected: map[int]string{}, fix: fix}
	c.parseOwners()
	c.compare(all, baseline)
	c.staleComments()
	if fix {
		if e := os.WriteFile(path, []byte(c.rewritten()), 0644); e != nil {
			return "", 1, e
		}
	}
	return strings.Join(c.messages, "\n") + "\n", c.code, nil
}

// parseOwners reads each "file | owner | reason" line, rejecting malformed,
// duplicate, unknown-owner and empty-reason lines.
func (c *migrationCheck) parseOwners() {
	for i, l := range c.lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		p := strings.SplitN(l, "|", 3)
		if len(p) != 3 {
			c.fail(fmt.Sprintf("line %d: expected file | owner | reason", i+1))
			continue
		}
		for j := range p {
			p[j] = strings.TrimSpace(p[j])
		}
		if _, ok := c.confirmed[p[0]]; ok {
			c.fail("duplicate file: " + p[0])
		}
		c.confirmed[p[0]] = migration{Name: p[0], Owner: p[1], Reason: p[2]}
		c.indices[p[0]] = i
		if p[1] != "platform" && p[1] != "hack" && p[1] != "mixed" {
			c.fail("invalid owner: " + p[0] + ": " + p[1])
		}
		if p[2] == "" {
			c.fail("empty reason: " + p[0])
		}
	}
}

// compare checks every migration has a line, notes owners that differ from
// the proposal, checks each mixed entry's generated comment, and rejects
// lines for files that do not exist.
func (c *migrationCheck) compare(all []migration, baseline map[string]bool) {
	present := map[string]bool{}
	for _, m := range all {
		present[m.Name] = true
		confirmed, ok := c.confirmed[m.Name]
		if !ok {
			c.fail("missing file: " + m.Name + "; add a line to scripts/split/migration-owners.txt: " + m.Name + " | platform or hack | what it changes")
			continue
		}
		if confirmed.Owner != m.Owner {
			c.note(m.Name + ": confirmed " + confirmed.Owner + "; proposal " + m.Owner + " | " + m.Reason)
		}
		if confirmed.Owner == "mixed" {
			c.mixed(m, all, baseline)
		}
	}
	for _, n := range sortedKeys(c.confirmed) {
		if !present[n] {
			c.fail("file does not exist: " + n)
		}
	}
}

// mixed records the comment a mixed entry needs directly beneath it, and
// fails when it is missing or stale, unless fixing.
func (c *migrationCheck) mixed(m migration, all []migration, baseline map[string]bool) {
	comment := mixedComment(m, all, c.confirmed, baseline)
	c.note(m.Name + ": " + comment)
	idx := c.indices[m.Name]
	c.expected[idx] = comment
	if idx+1 >= len(c.lines) || c.lines[idx+1] != comment {
		if !c.fix {
			c.fail("missing or stale mixed comment; expected: " + comment)
		}
	}
}

// staleComments fails on generated comments not directly beneath their mixed
// entry, unless fixing.
func (c *migrationCheck) staleComments() {
	// A generated line is valid only directly beneath its mixed entry.
	for i, l := range c.lines {
		if strings.HasPrefix(l, "#   mixed:") && (i == 0 || c.expected[i-1] != l) {
			if !c.fix {
				c.fail("stale mixed comment: " + l)
			}
		}
	}
}

// rewritten is the file with every generated comment replaced by the
// expected one beneath its entry.
func (c *migrationCheck) rewritten() string {
	var rewritten []string
	for i, l := range c.lines {
		if strings.HasPrefix(l, "#   mixed:") {
			continue
		}
		rewritten = append(rewritten, l)
		if comment := c.expected[i]; comment != "" {
			rewritten = append(rewritten, comment)
		}
	}
	return strings.Join(rewritten, "\n")
}
func migrationColumns(root string, z schemaInfo) ([]string, error) {
	proposed, _ := proposeObjects(z)
	hackTables := map[string]bool{}
	for _, ob := range proposed {
		if ob.Kind == "table" {
			hackTables[ob.Name] = true
		}
	}
	files, e := filepath.Glob(filepath.Join(root, "host/db/migrations/*hack*.sql"))
	if e != nil {
		return nil, e
	}
	productFiles, e := filepath.Glob(filepath.Join(root, "products/hack/db/migrations/*hack*.sql"))
	if e != nil {
		return nil, e
	}
	files = append(files, productFiles...)
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
			end := definitionWordEnd(s, i)
			maskColumnName(b, s[i:end], i)
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

// definitionWordEnd is where the first word of a definition starting at i
// ends: after a quoted identifier's closing quote, or after its identifier
// bytes.
func definitionWordEnd(s string, i int) int {
	end := i
	if s[i] == '"' {
		end++
		for end < len(s) && s[end] != '"' {
			end++
		}
		if end < len(s) {
			end++
		}
		return end
	}
	for end < len(s) && identifier(s[end]) {
		end++
	}
	return end
}

// maskColumnName blanks word, at offset i of b, when it names a column rather
// than starting a table constraint.
func maskColumnName(b []byte, word string, i int) {
	switch strings.ToUpper(strings.Trim(word, `"`)) {
	case "CONSTRAINT", "PRIMARY", "FOREIGN", "UNIQUE", "CHECK", "EXCLUDE":
		return
	}
	for j := i; j < i+len(word); j++ {
		b[j] = ' '
	}
}
